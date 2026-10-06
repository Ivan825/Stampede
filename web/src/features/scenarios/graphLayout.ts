import { parse } from 'yaml';

/**
 * Builds a read-only graph of journeys and their steps from scenario YAML:
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

function layoutSteps(ctx: Ctx, steps: unknown, x: number, y: number, from: string[]): SeqResult {
  let prev = from;
  let width = 1;
  if (!Array.isArray(steps)) return { y, last: prev, width };
  for (const raw of steps) {
    if (!isObj(raw)) continue;
    ctx.steps++;
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
        },
        prev,
      );
      prev = [id];
      y += ROW_H;
    } else if ('think' in raw) {
      const id = add(
        ctx,
        { kind: 'think', x, y, title: `think ${str(raw.think) ?? ''}`.trim() },
        prev,
      );
      prev = [id];
      y += THINK_H;
    } else if (Array.isArray(raw.branch)) {
      const arms = raw.branch.filter(isObj);
      const total = arms.reduce((s, a) => s + (typeof a.weight === 'number' ? a.weight : 0), 0);
      const id = add(
        ctx,
        { kind: 'branch', x, y, title: name ?? 'branch', subtitle: `${arms.length} paths` },
        prev,
      );
      y += ROW_H;
      const lasts: string[] = [];
      let maxY = y;
      let armX = x;
      let armsWidth = 0;
      arms.forEach((arm, i) => {
        const w = typeof arm.weight === 'number' ? arm.weight : 0;
        const share = total > 0 ? `${Math.round((w / total) * 100)}%` : `w${w}`;
        const label = [str(arm.name) ?? `path ${i + 1}`, share].join(' · ');
        // Label the first edge of the arm by adding it to the arm's first node.
        const before = ctx.edges.length;
        const r = layoutSteps(ctx, arm.steps, armX, y, [id]);
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
      const id = add(ctx, { kind, x, y, title, subtitle }, prev);
      y += ROW_H;
      const r = layoutSteps(ctx, raw.steps, x, y, [id]);
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
  for (const j of journeys) {
    const w = typeof j.weight === 'number' ? j.weight : 1;
    const share = total > 0 ? `${Math.round((w / total) * 100)}%` : '0%';
    const jid = add(
      ctx,
      { kind: 'journey', x, y: top, title: str(j.name) ?? 'journey', subtitle: `weight ${w}` },
      [startId],
      share,
    );
    const r = layoutSteps(ctx, j.steps, x, top + ROW_H, [jid]);
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
