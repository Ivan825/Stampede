import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/base.css';
import { clsx } from 'clsx';
import { X } from 'lucide-react';
import { memo, useMemo, useState } from 'react';
import { Button } from '@/components/ui';
import { buildJourneyGraph, COL_W, nodeKey, type GraphNode, type GraphResult } from './graphLayout';

type FlowNode = Node<{ g: GraphNode; selected: boolean }, 'step'>;

const methodTone: Record<string, string> = {
  GET: 'text-info',
  POST: 'text-pass',
  PUT: 'text-warn',
  PATCH: 'text-warn',
  DELETE: 'text-fail',
};

const StepNode = memo(function StepNode({ data }: NodeProps<FlowNode>) {
  const g = data.g;
  const small = g.kind === 'think';
  return (
    <div
      className={clsx(
        'rounded-md border bg-surface text-left shadow-sm',
        small ? 'px-2 py-1' : 'px-2.5 py-1.5',
        g.kind === 'start' && 'border-accent',
        g.kind === 'journey' && 'border-line-strong bg-surface-2',
        (g.kind === 'branch' || g.kind === 'loop' || g.kind === 'while' || g.kind === 'group') &&
          'border-dashed border-line-strong',
        (g.kind === 'request' || g.kind === 'think') && 'border-line',
        data.selected && 'ring-2 ring-accent',
      )}
      style={{ width: small ? 150 : COL_W - 30 }}
    >
      <Handle
        type="target"
        position={Position.Top}
        className="!size-1.5 !border-0 !bg-line-strong"
      />
      {small ? (
        <div className="truncate font-mono text-[11px] text-muted">{g.title}</div>
      ) : (
        <>
          <div className="flex items-center gap-1.5 truncate text-[12px] font-medium text-fg">
            {g.method && (
              <span className={clsx('font-mono text-[10px] font-bold', methodTone[g.method])}>
                {g.method}
              </span>
            )}
            {!g.method && g.kind !== 'request' && (
              <span className="font-mono text-[10px] tracking-wide text-muted uppercase">
                {g.kind}
              </span>
            )}
            <span className="truncate">{g.title}</span>
          </div>
          {g.subtitle && (
            <div className="truncate font-mono text-[10.5px] text-muted">{g.subtitle}</div>
          )}
        </>
      )}
      <Handle
        type="source"
        position={Position.Bottom}
        className="!size-1.5 !border-0 !bg-line-strong"
      />
    </div>
  );
});

const nodeTypes = { step: StepNode };

function flowNodes(result: GraphResult, selected: string | null): FlowNode[] {
  if (!result.ok) return [];
  return result.graph.nodes.map((g) => ({
    id: g.id,
    type: 'step',
    position: { x: g.kind === 'think' ? g.x + (COL_W - 30 - 150) / 2 : g.x, y: g.y },
    data: { g, selected: selected != null && nodeKey(g) === selected },
    draggable: false,
    connectable: false,
    selectable: nodeKey(g) != null,
    ariaLabel:
      g.kind === 'request' ? `${g.method ?? ''} ${g.title}`.trim() : `${g.kind} ${g.title}`,
  }));
}

const kindLabel: Record<GraphNode['kind'], string> = {
  start: 'Scenario',
  journey: 'Journey',
  request: 'HTTP request',
  think: 'Think',
  branch: 'Branch',
  loop: 'Loop',
  while: 'While',
  group: 'Group',
};

/** What the selected step or journey is, read-only. */
function StepDetails({ node, onClose }: { node: GraphNode; onClose: () => void }) {
  const rows: [string, string | undefined][] = [
    ['Method', node.method],
    ['URL', node.fields?.url],
    ['Name', node.fields?.name],
    ['Duration', node.fields?.think],
    ['Group', node.fields?.group],
    ['Where', node.subtitle && !node.fields?.url ? node.subtitle : undefined],
  ];
  const shown = rows.filter((r): r is [string, string] => !!r[1]);
  return (
    <aside
      className="flex w-64 shrink-0 flex-col gap-3 overflow-y-auto border-l border-line bg-surface p-3 text-[13px]"
      aria-label="Selected step"
    >
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="label-caps">{kindLabel[node.kind]}</div>
          <div className="truncate font-medium" title={node.title}>
            {node.title}
          </div>
        </div>
        <Button size="sm" variant="ghost" aria-label="Close" onClick={onClose}>
          <X className="size-3.5" aria-hidden />
        </Button>
      </div>
      {shown.length > 0 && (
        <dl className="grid grid-cols-[4.5rem_1fr] gap-x-2 gap-y-1.5 text-xs">
          {shown.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-muted">{k}</dt>
              <dd className="font-mono break-all">{v}</dd>
            </div>
          ))}
        </dl>
      )}
    </aside>
  );
}

/**
 * The journeys as a read-only graph: a start node fans out to each journey
 * by weight, steps chain downwards, branches split into columns and loops
 * draw a back edge. Select a node to see its details.
 */
export function JourneyGraphView({ yaml }: { yaml: string }) {
  const result = useMemo(() => buildJourneyGraph(yaml), [yaml]);
  const [selected, setSelected] = useState<string | null>(null);
  const nodes = useMemo(() => flowNodes(result, selected), [result, selected]);

  const edges = useMemo<Edge[]>(() => {
    if (!result.ok) return [];
    return result.graph.edges.map((e) => ({
      id: e.id,
      source: e.source,
      target: e.target,
      label: e.label,
      type: e.back ? 'smoothstep' : 'default',
      animated: !!e.back,
      markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14 },
      style: e.back ? { strokeDasharray: '4 3' } : undefined,
      labelStyle: { fontSize: 10, fontFamily: 'var(--font-mono)', fill: 'var(--muted)' },
      labelBgStyle: { fill: 'var(--surface)' },
    }));
  }, [result]);

  if (!result.ok) {
    return <p className="p-4 text-[13px] text-muted">{result.error}</p>;
  }
  const current = selected ? result.graph.nodes.find((g) => nodeKey(g) === selected) : undefined;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <p className="num border-b border-line px-3 py-1.5 text-xs text-muted">
        {result.graph.journeys} journeys · {result.graph.steps} steps
        <span className="font-sans"> · Select a step to see its details.</span>
      </p>
      <div className="flex min-h-0 flex-1">
        <div className="min-h-0 min-w-0 flex-1" role="group" aria-label="Journey graph">
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            onNodeClick={(_, n) => setSelected(nodeKey(n.data.g))}
            onPaneClick={() => setSelected(null)}
            fitView
            fitViewOptions={{ padding: 0.15, maxZoom: 1 }}
            minZoom={0.2}
            nodesDraggable={false}
            nodesConnectable={false}
            elementsSelectable
          >
            <Background gap={16} size={1} color="var(--line)" />
            <Controls showInteractive={false} />
          </ReactFlow>
        </div>
        {current && <StepDetails node={current} onClose={() => setSelected(null)} />}
      </div>
    </div>
  );
}
