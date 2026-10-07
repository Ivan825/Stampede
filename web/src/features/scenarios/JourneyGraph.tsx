import {
  applyNodeChanges,
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeChange,
  type NodeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/base.css';
import { clsx } from 'clsx';
import { memo, useMemo, useState } from 'react';
import {
  buildJourneyGraph,
  COL_W,
  dropIndex,
  nodeKey,
  pathKey,
  textHash,
  type GraphNode,
  type GraphResult,
} from './graphLayout';
import { moveStep } from './scenarioEdit';
import { StepPanel } from './StepPanel';

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

function flowNodes(result: GraphResult, editable: boolean, selected: string | null): FlowNode[] {
  if (!result.ok) return [];
  return result.graph.nodes.map((g) => ({
    id: g.id,
    type: 'step',
    position: { x: g.kind === 'think' ? g.x + (COL_W - 30 - 150) / 2 : g.x, y: g.y },
    data: { g, selected: selected != null && nodeKey(g) === selected },
    draggable: editable && g.seq != null,
    connectable: false,
    selectable: nodeKey(g) != null,
    ariaLabel:
      g.kind === 'request' ? `${g.method ?? ''} ${g.title}`.trim() : `${g.kind} ${g.title}`,
  }));
}

/**
 * The journeys as a graph. With `onChange` it also edits the YAML: drag a
 * step to reorder it among its siblings, select a node to change it, add
 * steps or remove it in the side panel. Every edit parses the YAML,
 * changes it and prints it again, so the YAML stays the single source of
 * truth and the editor and the graph never disagree.
 */
export function JourneyGraphView({
  yaml,
  onChange,
  readOnly,
}: {
  yaml: string;
  onChange?: (yaml: string) => void;
  readOnly?: boolean;
}) {
  const editable = !!onChange && !readOnly;
  const result = useMemo(() => buildJourneyGraph(yaml), [yaml]);
  const [selected, setSelected] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const laidOut = useMemo(
    () => flowNodes(result, editable, selected),
    [result, editable, selected],
  );
  const [nodes, setNodes] = useState<FlowNode[]>(laidOut);
  const [base, setBase] = useState(laidOut);
  // Every new layout (a YAML change, a selection) replaces dragged positions.
  if (base !== laidOut) {
    setBase(laidOut);
    setNodes(laidOut);
  }

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
  const graphNodes = result.graph.nodes;
  const current = selected ? graphNodes.find((g) => nodeKey(g) === selected) : undefined;

  /** Applies an edit; `select` picks the node to select afterwards. */
  const apply = (edit: () => string, select?: string | null) => {
    try {
      const next = edit();
      setError(null);
      if (select !== undefined) setSelected(select);
      if (next !== yaml) onChange?.(next);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const onDragStop = (_: unknown, node: FlowNode) => {
    const g = node.data.g;
    if (!g.seq || !editable) return;
    const key = pathKey(g.seq.path);
    const siblings = graphNodes.filter((s) => s.seq && pathKey(s.seq.path) === key);
    const to = dropIndex(siblings, g, node.position.y);
    if (to === g.seq.index) {
      setNodes(laidOut);
      return;
    }
    const seq = g.seq;
    apply(() => moveStep(yaml, seq.path, seq.index, to), pathKey([...seq.path, to]));
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <p className="num border-b border-line px-3 py-1.5 text-xs text-muted">
        {result.graph.journeys} journeys · {result.graph.steps} steps
        {editable && (
          <span className="font-sans">
            {' '}
            · Drag a step to reorder it; select one to edit it. Graph edits rewrite the YAML:
            comments are kept, spacing may change.
          </span>
        )}
      </p>
      <div className="flex min-h-0 flex-1">
        <div className="min-h-0 min-w-0 flex-1" role="group" aria-label="Journey graph">
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            onNodesChange={(changes: NodeChange<FlowNode>[]) =>
              setNodes((ns) => applyNodeChanges(changes, ns))
            }
            onNodeClick={(_, n) => setSelected(nodeKey(n.data.g))}
            onPaneClick={() => setSelected(null)}
            onNodeDragStop={onDragStop}
            fitView
            fitViewOptions={{ padding: 0.15, maxZoom: 1 }}
            minZoom={0.2}
            nodesDraggable={editable}
            nodesConnectable={false}
            elementsSelectable
          >
            <Background gap={16} size={1} color="var(--line)" />
            <Controls showInteractive={false} />
          </ReactFlow>
        </div>
        {current && (
          <StepPanel
            key={`${selected}:${textHash(yaml)}`}
            node={current}
            nodes={graphNodes}
            yaml={yaml}
            editable={editable}
            error={error}
            apply={apply}
            onClose={() => setSelected(null)}
          />
        )}
      </div>
    </div>
  );
}
