import {
  isMap,
  isScalar,
  isSeq,
  parseDocument,
  Scalar,
  stringify,
  type Document,
  type Pair,
  type YAMLMap,
  type YAMLSeq,
} from 'yaml';

/**
 * Structural edits to scenario YAML for the journey graph. The YAML stays
 * the single source of truth: each edit parses the text, finds the nodes
 * it changes and rewrites only their lines, so comments, blank lines and
 * the rest of the file are left as they were. Steps written in flow style
 * (`steps: [{get: /}]`) and other unusual layouts fall back to printing
 * the whole document again, which keeps comments but may change spacing.
 */

/** A path into the document: keys and sequence indexes. */
export type YamlPath = (string | number)[];

export const httpMethods = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'] as const;
export type HttpMethod = (typeof httpMethods)[number];

export type NewStepKind = 'request' | 'think' | 'group';

export class EditError extends Error {}

/** A text edit could not be made without reprinting; use the document. */
class Fallback extends Error {}

function parse(yamlText: string): Document {
  const doc = parseDocument(yamlText);
  if (doc.errors.length > 0) {
    throw new EditError(`Fix the YAML first: ${doc.errors[0]!.message.split('\n')[0]}`);
  }
  return doc;
}

function print(doc: Document): string {
  // lineWidth 0 never folds long strings onto new lines.
  return doc.toString({ lineWidth: 0 });
}

function seqAt(doc: Document, path: YamlPath): YAMLSeq {
  const node = doc.getIn(path, true);
  if (!isSeq(node)) throw new EditError('That list of steps no longer exists.');
  return node;
}

function mapAt(doc: Document, path: YamlPath): YAMLMap {
  const node = doc.getIn(path, true);
  if (!isMap(node)) throw new EditError('That step no longer exists.');
  return node;
}

// ---------------------------------------------------------------- text helpers

const lineStart = (t: string, pos: number) => t.lastIndexOf('\n', pos - 1) + 1;
const lineEnd = (t: string, pos: number) => {
  const i = t.indexOf('\n', pos);
  return i < 0 ? t.length : i + 1;
};
const column = (t: string, pos: number) => pos - lineStart(t, pos);

interface Edit {
  from: number;
  to: number;
  text: string;
}

function applyEdits(t: string, edits: Edit[]): string {
  let out = t;
  for (const e of [...edits].sort((a, b) => b.from - a.from)) {
    out = out.slice(0, e.from) + e.text + out.slice(e.to);
  }
  return out;
}

/** Checks that a text edit produced valid YAML; otherwise falls back. */
function checked(t: string): string {
  if (parseDocument(t).errors.length > 0) throw new Fallback();
  return t;
}

/** A scalar's value as YAML text on one line, quoted when it needs it. */
function scalarText(v: string): string {
  const s = stringify(v, { lineWidth: 0 }).replace(/\n$/, '');
  if (s.includes('\n')) throw new Fallback();
  return s;
}

function rangeOf(node: { range?: [number, number, number] | null }): [number, number, number] {
  if (!node.range) throw new Fallback();
  return node.range;
}

/** Where each item of a block sequence starts and where the last one ends. */
function blocks(t: string, seq: YAMLSeq) {
  if (seq.flow || seq.items.length === 0) throw new Fallback();
  const starts = seq.items.map((item) => {
    const [s] = rangeOf(item as { range?: [number, number, number] });
    const dash = t.lastIndexOf('-', s - 1);
    if (dash < 0 || t.slice(dash + 1, s).trim() !== '') throw new Fallback();
    let start = lineStart(t, dash);
    // Comment lines right above an item move with it.
    while (start > 0) {
      const prev = lineStart(t, start - 1);
      if (!t.slice(prev, start).trim().startsWith('#')) break;
      start = prev;
    }
    return { start, dash };
  });
  const last = seq.items[seq.items.length - 1] as { range?: [number, number, number] };
  const end = lineEnd(t, Math.max(rangeOf(last)[1] - 1, 0));
  const texts = starts.map((s, i) => {
    let b = t.slice(s.start, i + 1 < starts.length ? starts[i + 1]!.start : end);
    if (!b.endsWith('\n')) b += '\n';
    return b;
  });
  return { starts, end, texts, indent: column(t, starts[0]!.dash) };
}

/** The lines of a new list item at an indentation. */
function itemText(value: unknown, indent: number): string {
  const pad = ' '.repeat(indent);
  return stringify(value, { lineWidth: 0 })
    .replace(/\n$/, '')
    .split('\n')
    .map((line, i) => (i === 0 ? `${pad}- ${line}` : `${pad}  ${line}`))
    .join('\n')
    .concat('\n');
}

function newStepValue(kind: NewStepKind): Record<string, unknown> {
  switch (kind) {
    case 'request':
      return { name: 'new request', get: '/' };
    case 'think':
      return { think: '1s' };
    case 'group':
      return { group: 'new group', steps: [{ name: 'new request', get: '/' }] };
  }
}

/** Runs a text edit, or the document edit when the text edit cannot apply. */
function edit(yamlText: string, text: (doc: Document) => string, viaDoc: (doc: Document) => void) {
  const doc = parse(yamlText);
  try {
    return checked(text(doc));
  } catch (e) {
    if (!(e instanceof Fallback)) throw e;
    const again = parse(yamlText);
    viaDoc(again);
    return print(again);
  }
}

// ---------------------------------------------------------------- edits

/** Moves the step at `from` to index `to` within the same list of steps. */
export function moveStep(yamlText: string, seqPath: YamlPath, from: number, to: number): string {
  const seqLen = seqAt(parse(yamlText), seqPath).items.length;
  if (from < 0 || from >= seqLen) throw new EditError('That step no longer exists.');
  const target = Math.max(0, Math.min(seqLen - 1, to));
  if (target === from) return yamlText;
  return edit(
    yamlText,
    (doc) => {
      const b = blocks(yamlText, seqAt(doc, seqPath));
      const order = [...b.texts];
      const [moved] = order.splice(from, 1);
      order.splice(target, 0, moved!);
      return yamlText.slice(0, b.starts[0]!.start) + order.join('') + yamlText.slice(b.end);
    },
    (doc) => {
      const seq = seqAt(doc, seqPath);
      const [item] = seq.items.splice(from, 1);
      seq.items.splice(target, 0, item);
    },
  );
}

/** Removes the step at `index` from a list of steps. */
export function removeStep(yamlText: string, seqPath: YamlPath, index: number): string {
  const seqLen = seqAt(parse(yamlText), seqPath).items.length;
  if (index < 0 || index >= seqLen) throw new EditError('That step no longer exists.');
  return edit(
    yamlText,
    (doc) => {
      if (seqLen === 1) throw new Fallback(); // leaves `steps: []`
      const b = blocks(yamlText, seqAt(doc, seqPath));
      const from = b.starts[index]!.start;
      const to = index + 1 < seqLen ? b.starts[index + 1]!.start : b.end;
      return yamlText.slice(0, from) + yamlText.slice(to);
    },
    (doc) => {
      seqAt(doc, seqPath).items.splice(index, 1);
    },
  );
}

/**
 * Inserts a new step into a list of steps at `index` (the end when it is
 * past the last), creating the list when the journey or block has none.
 */
export function insertStep(
  yamlText: string,
  seqPath: YamlPath,
  index: number,
  kind: NewStepKind,
): string {
  const value = newStepValue(kind);
  return edit(
    yamlText,
    (doc) => {
      const node = doc.getIn(seqPath, true);
      if (node == null) {
        // No steps yet: add the key after the journey's or block's last line.
        const parent = mapAt(doc, seqPath.slice(0, -1));
        if (parent.flow || parent.items.length === 0) throw new Fallback();
        const first = parent.items[0]!.key as { range?: [number, number, number] };
        const indent = column(yamlText, rangeOf(first)[0]);
        const at = lineEnd(yamlText, Math.max(rangeOf(parent)[1] - 1, 0));
        const lead = at > 0 && yamlText[at - 1] !== '\n' ? '\n' : '';
        const text = `${lead}${' '.repeat(indent)}${String(seqPath[seqPath.length - 1])}:\n${itemText(value, indent + 2)}`;
        return yamlText.slice(0, at) + text + yamlText.slice(at);
      }
      if (!isSeq(node)) throw new EditError('That list of steps no longer exists.');
      const b = blocks(yamlText, node);
      const at = Math.max(0, Math.min(node.items.length, index));
      const pos = at < node.items.length ? b.starts[at]!.start : b.end;
      const lead = pos > 0 && yamlText[pos - 1] !== '\n' ? '\n' : '';
      return yamlText.slice(0, pos) + lead + itemText(value, b.indent) + yamlText.slice(pos);
    },
    (doc) => {
      let seq = doc.getIn(seqPath, true);
      if (seq == null) {
        mapAt(doc, seqPath.slice(0, -1)).set(seqPath[seqPath.length - 1], doc.createNode([]));
        seq = doc.getIn(seqPath, true);
      }
      if (!isSeq(seq)) throw new EditError('That list of steps no longer exists.');
      seq.items.splice(Math.max(0, Math.min(seq.items.length, index)), 0, doc.createNode(value));
    },
  );
}

const isMethodPair = (p: Pair) =>
  isScalar(p.key) && httpMethods.includes(String(p.key.value).toUpperCase() as HttpMethod);

function scalarPair(m: YAMLMap, key: string): Pair<Scalar, Scalar> | undefined {
  const p = m.items.find((x) => isScalar(x.key) && x.key.value === key);
  return p as Pair<Scalar, Scalar> | undefined;
}

/** Text edits that replace a plain or quoted one-line scalar. */
function replaceScalar(t: string, node: unknown, value: string): Edit {
  if (!isScalar(node)) throw new Fallback();
  const type = node.type;
  if (type !== 'PLAIN' && type !== 'QUOTE_DOUBLE' && type !== 'QUOTE_SINGLE') throw new Fallback();
  const [from, to] = rangeOf(node);
  if (t.slice(from, to).includes('\n')) throw new Fallback();
  return { from, to, text: scalarText(value) };
}

/** Text edits that set, add or remove a step's name. */
function nameEdits(t: string, m: YAMLMap, key: string, value: string): Edit[] {
  const v = value.trim();
  const pair = scalarPair(m, key);
  if (m.flow) throw new Fallback();
  if (pair && v) return [replaceScalar(t, pair.value, v)];
  if (pair && !v) {
    const [kFrom] = rangeOf(pair.key);
    const next = m.items[m.items.indexOf(pair) + 1];
    if (!next) throw new Fallback();
    const [nFrom] = rangeOf(next.key as Scalar);
    // On the "- " line, keep the dash; otherwise drop the whole line.
    const onDash = t.slice(lineStart(t, kFrom), kFrom).trim() === '-';
    return [
      {
        from: onDash ? kFrom : lineStart(t, kFrom),
        to: onDash ? nFrom : lineStart(t, nFrom),
        text: '',
      },
    ];
  }
  if (!pair && v) {
    const first = m.items[0]!.key as Scalar;
    const [fFrom] = rangeOf(first);
    return [
      { from: fFrom, to: fFrom, text: `${key}: ${scalarText(v)}\n${' '.repeat(column(t, fFrom))}` },
    ];
  }
  return [];
}

/** Sets a request step's method, URL and name. An empty name is removed. */
export function updateRequest(
  yamlText: string,
  stepPath: YamlPath,
  change: { method: HttpMethod; url: string; name: string },
): string {
  const check = mapAt(parse(yamlText), stepPath);
  if (!check.items.some(isMethodPair)) throw new EditError('That step is not an HTTP request.');
  const key = change.method.toLowerCase();
  return edit(
    yamlText,
    (doc) => {
      const m = mapAt(doc, stepPath);
      const pair = m.items.find(isMethodPair)!;
      const [kFrom, kTo] = rangeOf(pair.key as Scalar);
      return applyEdits(yamlText, [
        { from: kFrom, to: kTo, text: key },
        replaceScalar(yamlText, pair.value, change.url),
        ...nameEdits(yamlText, m, 'name', change.name),
      ]);
    },
    (doc) => {
      const m = mapAt(doc, stepPath);
      const pair = m.items.find(isMethodPair)!;
      // Keep the pair, and so its position and comments; change its key.
      pair.key = isScalar(pair.key) ? Object.assign(pair.key, { value: key }) : new Scalar(key);
      if (isScalar(pair.value)) pair.value.value = change.url;
      else pair.value = doc.createNode(change.url);
      const name = change.name.trim();
      if (!name) m.delete('name');
      else if (m.has('name')) m.set('name', name);
      else m.items.unshift(doc.createPair('name', name));
    },
  );
}

function updateValue(
  yamlText: string,
  stepPath: YamlPath,
  key: string,
  value: string,
  what: string,
) {
  if (!mapAt(parse(yamlText), stepPath).has(key)) throw new EditError(`That step is not ${what}.`);
  return edit(
    yamlText,
    (doc) =>
      applyEdits(yamlText, [
        replaceScalar(yamlText, mapAt(doc, stepPath).get(key, true), value.trim()),
      ]),
    (doc) => {
      mapAt(doc, stepPath).set(key, value.trim());
    },
  );
}

/** Sets a think step's duration, such as "2s" or "1s..3s". */
export function updateThink(yamlText: string, stepPath: YamlPath, duration: string): string {
  return updateValue(yamlText, stepPath, 'think', duration, 'a think step');
}

/** Sets a group's name. */
export function updateGroup(yamlText: string, stepPath: YamlPath, name: string): string {
  return updateValue(yamlText, stepPath, 'group', name, 'a group');
}
