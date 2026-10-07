/**
 * A small YAML highlighter for the read-only scenario view: it splits each
 * line into keys, strings, numbers, keywords, comments and ${…}
 * interpolations. It is a display aid, not a parser, so odd YAML only
 * loses colour, never text.
 */

export type TokenKind =
  | 'plain'
  | 'key'
  | 'string'
  | 'number'
  | 'keyword'
  | 'comment'
  | 'punct'
  | 'interp';

export interface Token {
  kind: TokenKind;
  text: string;
}

const keywords = new Set(['true', 'false', 'null', '~', 'yes', 'no', 'on', 'off']);
const numberRe = /^[-+]?(\d[\d_]*(\.\d+)?([eE][-+]?\d+)?|0x[\da-fA-F]+|\.inf|\.nan)$/;

/** Where a comment starts: a # at the line start or after a space, outside quotes. */
function commentStart(line: string): number {
  let quote: string | null = null;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (quote) {
      if (c === '\\' && quote === '"') i++;
      else if (c === quote) quote = null;
    } else if (c === '"' || c === "'") {
      if (i === 0 || /[\s:[{,-]/.test(line[i - 1]!)) quote = c;
    } else if (c === '#' && (i === 0 || /\s/.test(line[i - 1]!))) {
      return i;
    }
  }
  return -1;
}

/** Splits text on ${…} interpolations. */
function withInterpolations(text: string, kind: TokenKind): Token[] {
  const out: Token[] = [];
  const re = /\$\{[^}]*\}/g;
  let last = 0;
  for (let m = re.exec(text); m; m = re.exec(text)) {
    if (m.index > last) out.push({ kind, text: text.slice(last, m.index) });
    out.push({ kind: 'interp', text: m[0] });
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push({ kind, text: text.slice(last) });
  return out;
}

/** Tokens of a scalar value, or of the text after a key. */
function valueTokens(text: string): Token[] {
  const lead = /^\s*/.exec(text)![0];
  const body = text.slice(lead.length);
  const tail = /\s*$/.exec(body)![0];
  const v = body.slice(0, body.length - tail.length);
  const out: Token[] = lead ? [{ kind: 'plain', text: lead }] : [];
  if (!v) {
    // Nothing to colour.
  } else if (v.startsWith('"') || v.startsWith("'")) {
    out.push(...withInterpolations(v, 'string'));
  } else if (numberRe.test(v)) {
    out.push({ kind: 'number', text: v });
  } else if (keywords.has(v.toLowerCase())) {
    out.push({ kind: 'keyword', text: v });
  } else if (/^[|>][-+]?\d*$/.test(v) || /^[&*!][\w-]+$/.test(v)) {
    out.push({ kind: 'punct', text: v });
  } else if (/^[[{]/.test(v)) {
    // Flow collections: colour keys inside them loosely.
    const parts = v.split(/([[\]{},]|:\s)/);
    for (const p of parts) {
      if (!p) continue;
      if (/^([[\]{},]|:\s)$/.test(p)) out.push({ kind: 'punct', text: p });
      else out.push(...valueTokens(p));
    }
  } else {
    out.push(...withInterpolations(v, 'string'));
  }
  if (tail) out.push({ kind: 'plain', text: tail });
  return out;
}

const keyRe = /^(\s*(?:-\s+)*)("[^"]*"|'[^']*'|[^\s#'"[{][^:#]*?)(\s*:)(?=\s|$)/;

/** Splits one line of YAML into coloured tokens. */
export function tokenizeLine(line: string): Token[] {
  const c = commentStart(line);
  const code = c >= 0 ? line.slice(0, c) : line;
  const comment = c >= 0 ? line.slice(c) : '';
  const out: Token[] = [];

  if (/^\s*(---|\.\.\.)\s*$/.test(code)) {
    out.push({ kind: 'punct', text: code });
  } else {
    const k = keyRe.exec(code);
    if (k) {
      const [, lead, key, colon] = k;
      if (lead) out.push(...dashes(lead));
      out.push({ kind: 'key', text: key! });
      out.push({ kind: 'punct', text: colon! });
      out.push(...valueTokens(code.slice(k[0].length)));
    } else {
      const lead = /^(\s*(?:-\s+|-$)*)/.exec(code)![0];
      if (lead) out.push(...dashes(lead));
      out.push(...valueTokens(code.slice(lead.length)));
    }
  }
  if (comment) out.push({ kind: 'comment', text: comment });
  return out.filter((t) => t.text !== '');
}

/** Indentation and list dashes. */
function dashes(lead: string): Token[] {
  return lead
    .split(/(-)/)
    .filter(Boolean)
    .map((t) => ({ kind: t === '-' ? 'punct' : 'plain', text: t }));
}
