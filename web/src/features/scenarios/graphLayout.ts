import { parse } from 'yaml';

/** A path into the parsed YAML: keys and list indexes. */
export type YamlPath = (string | number)[];

/**
 * Builds a graph of journeys and their steps from scenario YAML:
 * a start node fans out to each journey by weight; steps chain downwards;
 * branches fan out into one column per arm; loop, while and group blocks
 * show their body with a back edge for repeats.
 */

export type GraphNodeKind =
  'start' | 'journey' | 'request' | 'think' | 'branch' | 'loop' | 'while' | 'group';

export interface GraphNode {
  id: string;
  kind: GraphNodeKind;
  x: number;
  y: number;
  title: string;
  subtitle?: string;
  method?: string;
  /** Where a step sits in the YAML: its list of steps and its index there. */
  seq?: { path: YamlPath; index: number };
  /** The list of steps a journey or block holds. */
  body?: YamlPath;
  /** The step's own fields, shown when it is selected. */
  fields?: { name?: string; url?: string; think?: string; group?: string };
}

/** The YAML path of a step node. */
export const stepPath = (n: GraphNode): YamlPath | undefined =>
  n.seq ? [...n.seq.path, n.seq.index] : undefined;

/** A stable key for a step across re-layouts: its YAML path. */
export const pathKey = (p: YamlPath) => p.join('/');

/** A key that identifies a step or journey node across re-layouts. */
export function nodeKey(g: GraphNode): string | null {
  const p = stepPath(g);
  if (p) return pathKey(p);
  if (g.kind === 'journey' && g.body) return `journey:${pathKey(g.body)}`;
  return null;
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
  label?: string;
  back?: boolean;
}

export interface JourneyGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
  journeys: number;
  steps: number;
}

export const COL_W = 230;
const ROW_H = 64;
const THINK_H = 40;
const methods = ['get', 'post', 'put', 'patch', 'delete', 'head', 'options'] as const;

type Obj = Record<string, unknown>;
const isObj = (v: unknown): v is Obj => typeof v === 'object' && v !== null && !Array.isArray(v);
const str = (v: unknown): string | undefined =>
  typeof v === 'string' ? v : typeof v === 'number' ? String(v) : undefined;

interface Ctx {
  nodes: GraphNode[];
  edges: GraphEdge[];
  n: number;
  steps: number;
}

function add(ctx: Ctx, node: Omit<GraphNode, 'id'>, from: string[], label?: string): string {
  const id = `n${ctx.n++}`;
  ctx.nodes.push({ id, ...node });
  for (const f of from) {
    ctx.edges.push({ id: `e${f}-${id}`, source: f, target: id, label });
  }
  return id;
}

interface SeqResult {
  y: number;
  last: string[];
  width: number;
}

function layoutSteps(
  ctx: Ctx,
  steps: unknown,
  x: number,
  y: number,
  from: string[],
  seqPath: YamlPath,
): SeqResult {
  let prev = from;
  let width = 1;
  if (!Array.isArray(steps)) return { y, last: prev, width };
  for (const [index, raw] of steps.entries()) {
    if (!isObj(raw)) continue;
    ctx.steps++;
    const seq = { path: seqPath, index };
    const here: YamlPath = [...seqPath, index];
    const name = str(raw.name);
    const method = methods.find((m) => typeof raw[m] === 'string');
    if (method) {
      const path = str(raw[method]) ?? '';
      const id = add(
        ctx,
        {
          kind: 'request',
          x,
          y,
          method: method.toUpperCase(),
          title: name ?? path,
          subtitle: name ? path : undefined,
          seq,
          fields: { name, url: path },
        },
        prev,
      );
      prev = [id];
      y += ROW_H;
    } else if ('think' in raw) {
      const id = add(
        ctx,
        {
          kind: 'think',
          x,
          y,
          title: `think ${str(raw.think) ?? ''}`.trim(),
          seq,
          fields: { think: str(raw.think) ?? '' },
        },
        prev,
      );
      prev = [id];
      y += THINK_H;
    } else if (Array.isArray(raw.branch)) {
      const arms = raw.branch.filter(isObj);
      const total = arms.reduce((s, a) => s + (typeof a.weight === 'number' ? a.weight : 0), 0);
      const id = add(
        ctx,
        {
          kind: 'branch',
          x,
          y,
          title: name ?? 'branch',
          subtitle: `${arms.length} paths`,
          seq,
        },
        prev,
      );
      y += ROW_H;
      const lasts: string[] = [];
      let maxY = y;
      let armX = x;
      let armsWidth = 0;
      raw.branch.forEach((arm: unknown, i: number) => {
        if (!isObj(arm)) return;
        const w = typeof arm.weight === 'number' ? arm.weight : 0;
        const share = total > 0 ? `${Math.round((w / total) * 100)}%` : `w${w}`;
        const label = [str(arm.name) ?? `path ${i + 1}`, share].join(' · ');
        // Label the first edge of the arm by adding it to the arm's first node.
        const before = ctx.edges.length;
        const r = layoutSteps(ctx, arm.steps, armX, y, [id], [...here, 'branch', i, 'steps']);
        const firstEdge = ctx.edges[before];
        if (firstEdge && firstEdge.source === id) firstEdge.label = label;
        lasts.push(...r.last);
        maxY = Math.max(maxY, r.y);
        armX += r.width * COL_W;
        armsWidth += r.width;
      });
      width = Math.max(width, armsWidth);
      prev = lasts.length ? lasts : [id];
      y = maxY;
    } else if (Array.isArray(raw.steps) && ('loop' in raw || 'while' in raw || 'group' in raw)) {
      const kind: GraphNodeKind = 'loop' in raw ? 'loop' : 'while' in raw ? 'while' : 'group';
      const title =
        kind === 'loop'
          ? `loop × ${str(raw.loop) ?? '?'}`
          : kind === 'while'
            ? 'while'
            : `group ${str(raw.group) ?? ''}`.trim();
      const subtitle =
        kind === 'while'
          ? `${str(raw.while) ?? ''}${raw.max != null ? ` (max ${str(raw.max)})` : ''}`
          : name;
      const body: YamlPath = [...here, 'steps'];
      const id = add(
        ctx,
        {
          kind,
          x,
          y,
          title,
          subtitle,
          seq,
          body,
          ...(kind === 'group' ? { fields: { group: str(raw.group) ?? '' } } : {}),
        },
        prev,
      );
      y += ROW_H;
      const r = layoutSteps(ctx, raw.steps, x, y, [id], body);
      if (kind !== 'group') {
        for (const l of r.last) {
          ctx.edges.push({ id: `b${l}-${id}`, source: l, target: id, back: true, label: 'repeat' });
        }
      }
      width = Math.max(width, r.width);
      prev = r.last;
      y = r.y;
    }
  }
  return { y, last: prev, width };
}

export type GraphResult = { ok: true; graph: JourneyGraph } | { ok: false; error: string };

export function buildJourneyGraph(yamlText: string): GraphResult {
  let doc: unknown;
  try {
    doc = parse(yamlText);
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message.split('\n')[0]! : String(err) };
  }
  if (!isObj(doc) || !Array.isArray(doc.journeys)) {
    return { ok: false, error: 'No journeys defined yet.' };
  }
  const ctx: Ctx = { nodes: [], edges: [], n: 0, steps: 0 };
  const journeys = doc.journeys.filter(isObj);
  const all: unknown[] = doc.journeys;
  const total = journeys.reduce((s, j) => s + (typeof j.weight === 'number' ? j.weight : 1), 0);
  const meta = isObj(doc.metadata) ? doc.metadata : {};
  const startId = add(
    ctx,
    {
      kind: 'start',
      x: 0,
      y: 0,
      title: str(meta.name) ?? 'scenario',
      subtitle: 'virtual user starts',
    },
    [],
  );
  let x = 0;
  const top = ROW_H + 30;
  for (const [ji, j] of all.entries()) {
    if (!isObj(j)) continue;
    const w = typeof j.weight === 'number' ? j.weight : 1;
    const share = total > 0 ? `${Math.round((w / total) * 100)}%` : '0%';
    const body: YamlPath = ['journeys', ji, 'steps'];
    const jid = add(
      ctx,
      {
        kind: 'journey',
        x,
        y: top,
        title: str(j.name) ?? 'journey',
        subtitle: `weight ${w}`,
        body,
      },
      [startId],
      share,
    );
    const r = layoutSteps(ctx, j.steps, x, top + ROW_H, [jid], body);
    x += r.width * COL_W + 30;
  }
  // Centre the start node over the journeys.
  const start = ctx.nodes[0]!;
  start.x = Math.max(0, (x - 30 - COL_W) / 2);
  return {
    ok: true,
    graph: { nodes: ctx.nodes, edges: ctx.edges, journeys: journeys.length, steps: ctx.steps },
  };
}
