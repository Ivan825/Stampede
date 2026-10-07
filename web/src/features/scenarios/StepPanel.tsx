import { ArrowDown, ArrowUp, Plus, Trash2, X } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import { Button, Field, Input, Select } from '@/components/ui';
import { pathKey, type GraphNode } from './graphLayout';
import {
  httpMethods,
  insertStep,
  moveStep,
  removeStep,
  updateGroup,
  updateRequest,
  updateThink,
  type HttpMethod,
  type NewStepKind,
  type YamlPath,
} from './scenarioEdit';

const kinds: [NewStepKind, string][] = [
  ['request', 'HTTP request'],
  ['think', 'Think'],
  ['group', 'Group'],
];

function AddButtons({ label, onAdd }: { label: string; onAdd: (kind: NewStepKind) => void }) {
  return (
    <div role="group" aria-label={label} className="flex flex-col gap-1">
      <span className="label-caps">{label}</span>
      <div className="flex flex-wrap gap-1">
        {kinds.map(([k, text]) => (
          <Button key={k} size="sm" onClick={() => onAdd(k)}>
            <Plus className="size-3.5" aria-hidden />
            {text}
          </Button>
        ))}
      </div>
    </div>
  );
}

function RequestForm({
  node,
  onSave,
}: {
  node: GraphNode;
  onSave: (c: { method: HttpMethod; url: string; name: string }) => void;
}) {
  const [method, setMethod] = useState<HttpMethod>((node.method as HttpMethod) ?? 'GET');
  const [url, setUrl] = useState(node.fields?.url ?? '');
  const [name, setName] = useState(node.fields?.name ?? '');
  const submit = (e: FormEvent) => {
    e.preventDefault();
    onSave({ method, url, name });
  };
  return (
    <form className="flex flex-col gap-2.5" onSubmit={submit} aria-label="Edit request">
      <Field label="Method">
        {(p) => (
          <Select {...p} value={method} onChange={(e) => setMethod(e.target.value as HttpMethod)}>
            {httpMethods.map((m) => (
              <option key={m}>{m}</option>
            ))}
          </Select>
        )}
      </Field>
      <Field label="URL" hint="A path joins the target's base URL.">
        {(p) => (
          <Input
            {...p}
            className="font-mono text-xs"
            required
            value={url}
            onChange={(e) => setUrl(e.target.value)}
          />
        )}
      </Field>
      <Field label="Name" hint="Optional; reports show it instead of the URL.">
        {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} />}
      </Field>
      <div>
        <Button type="submit" size="sm" variant="primary">
          Apply
        </Button>
      </div>
    </form>
  );
}

function OneFieldForm({
  label,
  hint,
  initial,
  formLabel,
  onSave,
}: {
  label: string;
  hint?: string;
  initial: string;
  formLabel: string;
  onSave: (v: string) => void;
}) {
  const [v, setV] = useState(initial);
  return (
    <form
      className="flex flex-col gap-2.5"
      aria-label={formLabel}
      onSubmit={(e) => {
        e.preventDefault();
        onSave(v);
      }}
    >
      <Field label={label} hint={hint}>
        {(p) => <Input {...p} required value={v} onChange={(e) => setV(e.target.value)} />}
      </Field>
      <div>
        <Button type="submit" size="sm" variant="primary">
          Apply
        </Button>
      </div>
    </form>
  );
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

/** Edits the selected step or journey of the journey graph. */
export function StepPanel({
  node,
  nodes,
  yaml,
  editable,
  error,
  apply,
  onClose,
}: {
  node: GraphNode;
  nodes: GraphNode[];
  yaml: string;
  editable: boolean;
  error: string | null;
  apply: (edit: () => string, select?: string | null) => void;
  onClose: () => void;
}) {
  const seq = node.seq;
  const here: YamlPath | undefined = seq ? [...seq.path, seq.index] : undefined;
  const siblings = seq
    ? nodes.filter((n) => n.seq && pathKey(n.seq.path) === pathKey(seq.path)).length
    : 0;
  const move = (to: number) => {
    if (!seq) return;
    apply(() => moveStep(yaml, seq.path, seq.index, to), pathKey([...seq.path, to]));
  };
  const insertAt = (path: YamlPath, index: number, kind: NewStepKind) =>
    apply(() => insertStep(yaml, path, index, kind), pathKey([...path, index]));
  const bodyLength = (path: YamlPath) =>
    nodes.filter((n) => n.seq && pathKey(n.seq.path) === pathKey(path)).length;

  return (
    <aside
      className="flex w-72 shrink-0 flex-col gap-4 overflow-y-auto border-l border-line bg-surface p-3 text-[13px]"
      aria-label="Selected step"
    >
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="label-caps">{kindLabel[node.kind]}</div>
          <div className="truncate font-medium">{node.title}</div>
        </div>
        <Button size="sm" variant="ghost" aria-label="Close" onClick={onClose}>
          <X className="size-3.5" aria-hidden />
        </Button>
      </div>

      {!editable && (
        <p className="text-xs text-muted">Read only. Edit the latest version to change steps.</p>
      )}

      {editable && node.kind === 'request' && here && (
        <RequestForm node={node} onSave={(c) => apply(() => updateRequest(yaml, here, c))} />
      )}
      {editable && node.kind === 'think' && here && (
        <OneFieldForm
          label="Duration"
          hint="Such as 2s, or 1s..3s for a random pause."
          formLabel="Edit think"
          initial={node.fields?.think ?? ''}
          onSave={(v) => apply(() => updateThink(yaml, here, v))}
        />
      )}
      {editable && node.kind === 'group' && here && (
        <OneFieldForm
          label="Group name"
          formLabel="Edit group"
          initial={node.fields?.group ?? ''}
          onSave={(v) => apply(() => updateGroup(yaml, here, v))}
        />
      )}
      {editable && (node.kind === 'branch' || node.kind === 'loop' || node.kind === 'while') && (
        <p className="text-xs text-muted">Change this block's settings in the YAML editor.</p>
      )}

      {editable && seq && (
        <div className="flex flex-col gap-1">
          <span className="label-caps">Order</span>
          <div className="flex gap-1">
            <Button size="sm" disabled={seq.index === 0} onClick={() => move(seq.index - 1)}>
              <ArrowUp className="size-3.5" aria-hidden /> Move up
            </Button>
            <Button
              size="sm"
              disabled={seq.index >= siblings - 1}
              onClick={() => move(seq.index + 1)}
            >
              <ArrowDown className="size-3.5" aria-hidden /> Move down
            </Button>
          </div>
        </div>
      )}
      {editable && node.body && (
        <AddButtons
          label={node.kind === 'journey' ? 'Add a step at the end' : 'Add a step inside'}
          onAdd={(k) => insertAt(node.body!, bodyLength(node.body!), k)}
        />
      )}
      {editable && seq && (
        <AddButtons label="Add a step after" onAdd={(k) => insertAt(seq.path, seq.index + 1, k)} />
      )}
      {editable && seq && (
        <div>
          <Button
            size="sm"
            variant="danger"
            onClick={() => apply(() => removeStep(yaml, seq.path, seq.index), null)}
          >
            <Trash2 className="size-3.5" aria-hidden /> Remove step
          </Button>
        </div>
      )}
      {error && (
        <p role="alert" className="rounded-md border border-fail/40 bg-fail-bg px-2 py-1.5 text-xs">
          {error}
        </p>
      )}
    </aside>
  );
}
