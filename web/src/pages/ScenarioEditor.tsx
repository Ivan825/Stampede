import * as Tabs from '@radix-ui/react-tabs';
import { Link, useNavigate, useParams, useSearch } from '@tanstack/react-router';
import { clsx } from 'clsx';
import { AlertTriangle, CheckCircle2, History, ListChecks, Play, Save, Trash2 } from 'lucide-react';
import { useState } from 'react';
import {
  useCreateScenario,
  useCreateScenarioVersion,
  useDeleteScenario,
  useMe,
  useScenario,
  useScenarioVersion,
  useScenarioVersions,
} from '@/api/queries';
import type { PlanSummary, Scenario } from '@/api/types';
import { Chip } from '@/components/chips';
import { Confirm } from '@/components/dialog';
import { useToast } from '@/components/toast';
import { Button, ErrorAlert, Input, Loading, Notice, Spinner } from '@/components/ui';
import { NewRunDialog } from '@/features/runs/NewRunDialog';
import { JourneyGraphView } from '@/features/scenarios/JourneyGraph';
import { useValidation } from '@/features/scenarios/useValidation';
import { YamlEditor, type EditorMarker } from '@/features/scenarios/YamlEditor';
import { dateTime, humanDuration, load, relativeTime } from '@/lib/format';
import { permissions } from '@/lib/roles';

export const TEMPLATE = `apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: my-scenario
  description: Browse the catalogue, sometimes buy.
  tags: [smoke]
target:
  baseURL: \${env.TARGET_URL}
journeys:
  - name: browse
    weight: 9
    steps:
      - get: /api/products?page=\${rand(1, 20)}
        check: { status: 200 }
        extract: { productId: "$.items[0].id" }
      - think: 1s..3s
      - get: /api/products/\${productId}
  - name: checkout
    weight: 1
    steps:
      - post: /api/cart
        json: { productId: 1, qty: 1 }
        check: { status: [200, 201] }
      - post: /api/checkout
load:
  mode: rate
  rate: 20/s
  duration: 2m
targets:
  - http.p95 < 300ms
  - errors < 1%
`;

const tabTrigger =
  'flex h-9 items-center gap-1.5 border-b-2 border-transparent px-1 text-[13px] text-muted hover:text-fg data-[state=active]:border-accent data-[state=active]:font-medium data-[state=active]:text-fg';

function PlanView({ plan }: { plan: PlanSummary | undefined }) {
  if (!plan) return <p className="p-4 text-[13px] text-muted">Fix the problems to see the plan.</p>;
  const rows: [string, string][] = [
    ['Executor', plan.executor],
    ['Mode', plan.mode === 'rate' ? 'rate (open model)' : 'vus (closed model)'],
    ['Shape', plan.shape ?? '–'],
    ['Peak load', load(plan.peak, plan.mode)],
    ['Duration', humanDuration(plan.durationSeconds)],
    ['Journeys', String(plan.journeys)],
    ['Request steps', String(plan.steps)],
  ];
  return (
    <dl className="grid grid-cols-[9rem_1fr] gap-y-2 p-4 text-[13px]">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-muted">{k}</dt>
          <dd className="num">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

function ProblemsView({
  serverProblems,
  markers,
  validating,
  validationError,
  onReveal,
}: {
  serverProblems: string[];
  markers: EditorMarker[];
  validating: boolean;
  validationError: unknown;
  onReveal: (line: number) => void;
}) {
  const none = serverProblems.length === 0 && markers.length === 0;
  return (
    <div className="flex flex-col gap-3 p-3 text-[13px]">
      <div className="flex items-center gap-2 text-xs text-muted">
        {validating ? (
          <>
            <Spinner /> Checking…
          </>
        ) : none && !validationError ? (
          <>
            <CheckCircle2 className="size-4 text-pass" aria-hidden /> No problems found.
          </>
        ) : null}
      </div>
      <ErrorAlert error={validationError} />
      {serverProblems.length > 0 && (
        <section>
          <h3 className="label-caps mb-1.5">Server validation</h3>
          <ul className="flex flex-col gap-1">
            {serverProblems.map((p, i) => (
              <li key={i} className="flex gap-2 rounded border border-line bg-surface px-2 py-1.5">
                <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-fail" aria-hidden />
                <span className="font-mono text-xs">{p}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {markers.length > 0 && (
        <section>
          <h3 className="label-caps mb-1.5">Schema</h3>
          <ul className="flex flex-col gap-1">
            {markers.map((m, i) => (
              <li key={i}>
                <button
                  type="button"
                  onClick={() => onReveal(m.line)}
                  className="flex w-full gap-2 rounded border border-line bg-surface px-2 py-1.5 text-left hover:border-line-strong"
                >
                  <AlertTriangle
                    className={clsx(
                      'mt-0.5 size-3.5 shrink-0',
                      m.severity === 'error' ? 'text-fail' : 'text-warn',
                    )}
                    aria-hidden
                  />
                  <span className="num shrink-0 text-xs text-muted">
                    {m.line}:{m.column}
                  </span>
                  <span className="text-xs">{m.message}</span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

function HistoryView({
  scenario,
  viewing,
  onView,
}: {
  scenario: Scenario;
  viewing: number | undefined;
  onView: (v: number | undefined) => void;
}) {
  const versions = useScenarioVersions(scenario.id);
  if (versions.isPending) return <Loading />;
  if (versions.error) return <ErrorAlert error={versions.error} className="m-3" />;
  return (
    <ol className="divide-y divide-line">
      {versions.data.map((v) => {
        const latest = v.version === scenario.latestVersion.version;
        const active = viewing ? viewing === v.version : latest;
        return (
          <li key={v.version}>
            <button
              type="button"
              className={clsx(
                'flex w-full flex-col gap-0.5 px-3 py-2 text-left hover:bg-surface-2',
                active && 'bg-surface-2',
              )}
              aria-current={active || undefined}
              onClick={() => onView(latest ? undefined : v.version)}
            >
              <span className="flex items-center gap-2 text-[13px]">
                <span className="num font-semibold">v{v.version}</span>
                {latest && <Chip tone="info">latest</Chip>}
                <span className="ml-auto text-xs text-muted" title={dateTime(v.createdAt)}>
                  {relativeTime(v.createdAt)}
                </span>
              </span>
              <span className="truncate text-xs text-muted">
                {v.message || 'No message'}
                {v.createdBy ? ` — ${v.createdBy}` : ''}
              </span>
            </button>
          </li>
        );
      })}
    </ol>
  );
}

function EditorLayout({
  projectId,
  scenario,
  initialYaml,
  viewing,
  onView,
}: {
  projectId: string;
  scenario?: Scenario;
  initialYaml: string;
  viewing?: number;
  onView?: (v: number | undefined) => void;
}) {
  const me = useMe();
  const can = permissions(me.role);
  const navigate = useNavigate();
  const toast = useToast();
  const createScenario = useCreateScenario(projectId);
  const createVersion = useCreateScenarioVersion(projectId, scenario?.id ?? '');
  const del = useDeleteScenario(projectId);
  const viewed = useScenarioVersion(scenario?.id ?? '', viewing);
  const readOnly = !can.editScenarios || viewing != null;

  const [yaml, setYaml] = useState(initialYaml);
  const [saved, setSaved] = useState(initialYaml);
  const [message, setMessage] = useState('');
  const [markers, setMarkers] = useState<EditorMarker[]>([]);
  const [tab, setTab] = useState('journeys');
  const [reveal, setReveal] = useState<{ line: number; n: number }>();
  const [newRun, setNewRun] = useState(false);

  const shownYaml = viewing != null ? (viewed.data?.yaml ?? '') : yaml;
  const validation = useValidation(shownYaml);
  const dirty = viewing == null && yaml !== saved;
  const problems = validation.result?.problems ?? [];
  const problemCount = problems.length + markers.filter((m) => m.severity !== 'info').length;
  const saving = createScenario.isPending || createVersion.isPending;
  const saveError = scenario ? createVersion.error : createScenario.error;

  const save = () => {
    if (readOnly || saving || (!dirty && scenario)) return;
    const body = { yaml, ...(message.trim() ? { message: message.trim() } : {}) };
    if (scenario) {
      createVersion.mutate(body, {
        onSuccess: (v) => {
          setSaved(yaml);
          setMessage('');
          toast.success(`Saved version ${v.version}.`);
        },
      });
    } else {
      createScenario.mutate(body, {
        onSuccess: (s) => {
          toast.success(`Created ${s.name}.`);
          void navigate({
            to: '/projects/$projectId/scenarios/$scenarioId',
            params: { projectId, scenarioId: s.id },
          });
        },
      });
    }
  };

  const resetKey = viewing != null ? `v${viewing}:${viewed.data ? 1 : 0}` : 'draft';
  const editorValue = viewing != null ? (viewed.data?.yaml ?? '') : yaml;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex flex-wrap items-center gap-3 border-b border-line bg-surface px-4 py-2">
        <div className="min-w-0">
          <div className="text-xs text-muted">
            <Link
              to="/projects/$projectId/scenarios"
              params={{ projectId }}
              className="hover:underline"
            >
              Scenarios
            </Link>{' '}
            /
          </div>
          <h1 className="flex items-center gap-2 truncate text-[15px] font-semibold">
            {scenario ? scenario.name : 'New scenario'}
            {scenario && (
              <span className="num text-xs font-normal text-muted">
                v{viewing ?? scenario.latestVersion.version}
              </span>
            )}
            {dirty && <Chip tone="warn">unsaved</Chip>}
            {readOnly && viewing == null && <Chip>read only</Chip>}
          </h1>
        </div>
        <div className="flex flex-1 flex-wrap items-center justify-end gap-2">
          {!readOnly && (
            <>
              <div className="w-64">
                <Input
                  aria-label="Version message"
                  placeholder={scenario ? 'Describe this change' : 'Initial version message'}
                  maxLength={500}
                  value={message}
                  onChange={(e) => setMessage(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && save()}
                />
              </div>
              <Button
                variant="primary"
                onClick={save}
                loading={saving}
                disabled={scenario ? !dirty : !yaml.trim()}
                title="Save (Ctrl/Cmd+S)"
              >
                <Save className="size-3.5" aria-hidden />
                {scenario ? 'Save version' : 'Create scenario'}
              </Button>
            </>
          )}
          {scenario && (
            <Link
              to="/projects/$projectId/scenarios/$scenarioId/coverage"
              params={{ projectId, scenarioId: scenario.id }}
              className="inline-flex h-8 items-center gap-2 rounded-md border border-line bg-surface px-3 text-sm font-medium hover:border-line-strong hover:bg-surface-2"
            >
              <ListChecks className="size-3.5" aria-hidden /> API coverage
            </Link>
          )}
          {scenario && can.startRuns && (
            <Button onClick={() => setNewRun(true)}>
              <Play className="size-3.5" aria-hidden /> Run
            </Button>
          )}
          {scenario && can.editScenarios && (
            <Confirm
              trigger={
                <Button variant="danger" aria-label="Delete scenario">
                  <Trash2 className="size-3.5" aria-hidden />
                </Button>
              }
              title={`Delete ${scenario.name}?`}
              description="All versions are deleted. Past runs keep their reports."
              confirmLabel="Delete scenario"
              destructive
              onConfirm={async () => {
                await del.mutateAsync(scenario.id);
                toast.success(`Deleted ${scenario.name}.`);
                void navigate({ to: '/projects/$projectId/scenarios', params: { projectId } });
              }}
            />
          )}
        </div>
      </div>
      {saveError ? (
        <div className="border-b border-line px-4 py-2">
          <ErrorAlert error={saveError} />
        </div>
      ) : null}
      {viewing != null && (
        <div className="flex items-center gap-3 border-b border-line bg-info-bg px-4 py-1.5 text-[13px]">
          <History className="size-4 text-info" aria-hidden />
          Viewing version {viewing} (read only).
          {can.editScenarios && viewed.data && (
            <Button
              size="sm"
              onClick={() => {
                setYaml(viewed.data.yaml);
                setMessage(`Restore v${viewing}`);
                onView?.(undefined);
              }}
            >
              Restore into editor
            </Button>
          )}
          <Button size="sm" variant="ghost" onClick={() => onView?.(undefined)}>
            Back to latest
          </Button>
        </div>
      )}
      <div className="grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[minmax(0,1fr)_minmax(360px,42%)]">
        <div className="min-h-[50vh] border-r border-line bg-surface">
          <YamlEditor
            value={editorValue}
            resetKey={resetKey}
            readOnly={readOnly}
            onChange={(v) => viewing == null && setYaml(v)}
            onSave={save}
            onMarkers={setMarkers}
            revealLine={reveal}
          />
        </div>
        <Tabs.Root value={tab} onValueChange={setTab} className="flex min-h-0 flex-col bg-bg">
          <Tabs.List
            className="flex gap-4 border-b border-line bg-surface px-3"
            aria-label="Scenario details"
          >
            <Tabs.Trigger value="journeys" className={tabTrigger}>
              Journeys
            </Tabs.Trigger>
            <Tabs.Trigger value="plan" className={tabTrigger}>
              Plan
            </Tabs.Trigger>
            <Tabs.Trigger value="problems" className={tabTrigger}>
              Problems
              <span
                className={clsx(
                  'num rounded-full px-1.5 text-[11px]',
                  problemCount ? 'bg-fail-bg text-fail' : 'bg-surface-2 text-muted',
                )}
              >
                {problemCount}
              </span>
            </Tabs.Trigger>
            {scenario && (
              <Tabs.Trigger value="history" className={tabTrigger}>
                History
              </Tabs.Trigger>
            )}
          </Tabs.List>
          <Tabs.Content value="journeys" className="min-h-[360px] flex-1">
            <JourneyGraphView yaml={shownYaml} />
          </Tabs.Content>
          <Tabs.Content value="plan" className="flex-1 overflow-y-auto">
            {validation.result?.valid === false && (
              <Notice tone="fail" className="m-3">
                The scenario has problems; the plan reflects the last valid parse, if any.
              </Notice>
            )}
            <PlanView plan={validation.result?.plan} />
          </Tabs.Content>
          <Tabs.Content value="problems" className="flex-1 overflow-y-auto">
            <ProblemsView
              serverProblems={problems}
              markers={markers}
              validating={validation.pending}
              validationError={validation.error}
              onReveal={(line) => setReveal((r) => ({ line, n: (r?.n ?? 0) + 1 }))}
            />
          </Tabs.Content>
          {scenario && (
            <Tabs.Content value="history" className="flex-1 overflow-y-auto">
              <HistoryView scenario={scenario} viewing={viewing} onView={(v) => onView?.(v)} />
            </Tabs.Content>
          )}
        </Tabs.Root>
      </div>
      {scenario && can.startRuns && (
        <NewRunDialog
          projectId={projectId}
          scenarioId={scenario.id}
          open={newRun}
          onOpenChange={setNewRun}
        />
      )}
    </div>
  );
}

export function NewScenarioPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/scenarios/new' });
  return <EditorLayout projectId={projectId} initialYaml={TEMPLATE} />;
}

export function ScenarioEditorPage() {
  const { projectId, scenarioId } = useParams({
    from: '/app/projects/$projectId/scenarios/$scenarioId',
  });
  const search = useSearch({ from: '/app/projects/$projectId/scenarios/$scenarioId' });
  const navigate = useNavigate({ from: '/projects/$projectId/scenarios/$scenarioId' });
  const scenario = useScenario(scenarioId);
  const latestYaml = scenario.data?.latestVersion.yaml;

  if (scenario.isPending) return <Loading />;
  if (scenario.error) return <ErrorAlert error={scenario.error} className="m-6" />;
  return (
    <EditorLayout
      key={scenarioId}
      projectId={projectId}
      scenario={scenario.data}
      initialYaml={latestYaml ?? ''}
      viewing={search.version}
      onView={(v) => void navigate({ search: v ? { version: v } : {} })}
    />
  );
}
