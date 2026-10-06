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
import { memo, useMemo } from 'react';
import { buildJourneyGraph, COL_W, type GraphNode } from './graphLayout';

type FlowNode = Node<{ g: GraphNode }, 'step'>;

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

export function JourneyGraphView({ yaml }: { yaml: string }) {
  const result = useMemo(() => buildJourneyGraph(yaml), [yaml]);
  const { nodes, edges } = useMemo(() => {
    if (!result.ok) return { nodes: [] as FlowNode[], edges: [] as Edge[] };
    const nodes: FlowNode[] = result.graph.nodes.map((g) => ({
      id: g.id,
      type: 'step',
      position: { x: g.kind === 'think' ? g.x + (COL_W - 30 - 150) / 2 : g.x, y: g.y },
      data: { g },
      draggable: false,
      connectable: false,
      selectable: false,
    }));
    const edges: Edge[] = result.graph.edges.map((e) => ({
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
    return { nodes, edges };
  }, [result]);

  if (!result.ok) {
    return <p className="p-4 text-[13px] text-muted">{result.error}</p>;
  }
  return (
    <div className="flex h-full flex-col">
      <p className="num border-b border-line px-3 py-1.5 text-xs text-muted">
        {result.graph.journeys} journeys · {result.graph.steps} steps
      </p>
      <div className="min-h-0 flex-1" aria-label="Journey graph" role="img">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          fitView
          fitViewOptions={{ padding: 0.15, maxZoom: 1 }}
          minZoom={0.2}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable={false}
        >
          <Background gap={16} size={1} color="var(--line)" />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>
    </div>
  );
}
