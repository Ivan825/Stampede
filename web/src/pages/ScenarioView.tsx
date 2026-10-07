import * as Tabs from '@radix-ui/react-tabs';
import { Link, useNavigate, useParams, useSearch } from '@tanstack/react-router';
import { clsx } from 'clsx';
import { AlertTriangle, CheckCircle2, History, ListChecks } from 'lucide-react';
import { useState } from 'react';
import {
  useProject,
  useScenario,
  useScenarioVersion,
  useScenarioVersions,
} from '@/api/queries';
import type { PlanSummary, Scenario } from '@/api/types';
import { Chip } from '@/components/chips';
import { CliHint } from '@/components/cliHint';
import { Button, ErrorAlert, Loading, Notice, Spinner } from '@/components/ui';
import { JourneyGraphView } from '@/features/scenarios/JourneyGraph';
import { useValidation } from '@/features/scenarios/useValidation';
import { YamlView } from '@/features/scenarios/YamlView';
import { cli } from '@/lib/cli';
import { dateTime, humanDuration, load, relativeTime } from '@/lib/format';

const tabTrigger =
  'flex h-9 items-center gap-1.5 border-b-2 border-transparent px-1 text-[13px] text-muted hover:text-fg data-[state=active]:border-accent data-[state=active]:font-medium data-[state=active]:text-fg';

function PlanView({ plan }: { plan: PlanSummary | undefined }) {
  if (!plan) return <p className="p-4 text-[13px] text-muted">No plan: the scenario has problems.</p>;
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
  problems,
  validating,
  validationError,
}: {
  problems: string[];
  validating: boolean;
  validationError: unknown;
}) {
  return (
    <div className="flex flex-col gap-3 p-3 text-[13px]">
      <div className="flex items-center gap-2 text-xs text-muted">
        {validating ? (
          <>
            <Spinner /> Checking…
          </>
        ) : problems.length === 0 && !validationError ? (
          <>
            <CheckCircle2 className="size-4 text-pass" aria-hidden /> No problems found.
          </>
        ) : null}
      </div>
      <ErrorAlert error={validationError} />
      {problems.length > 0 && (
        <ul className="flex flex-col gap-1" aria-label="Problems">
          {problems.map((p, i) => (
            <li key={i} className="flex gap-2 rounded border border-line bg-surface px-2 py-1.5">
              <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-fail" aria-hidden />
              <span className="font-mono text-xs">{p}</span>
            </li>
          ))}
        </ul>
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

function ScenarioLayout({
  projectId,
  scenario,
  viewing,
  onView,
}: {
  projectId: string;
  scenario: Scenario;
  viewing?: number;
  onView: (v: number | undefined) => void;
}) {
  const project = useProject(projectId);
  const viewed = useScenarioVersion(scenario.id, viewing);
  const [tab, setTab] = useState('journeys');
  const version = viewing != null ? viewed.data : scenario.latestVersion;
  const yaml = version?.yaml ?? '';
  const validation = useValidation(yaml, 0);
  const problems = validation.result?.problems ?? [];

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
            {scenario.name}
            <span className="num text-xs font-normal text-muted">
              v{viewing ?? scenario.latestVersion.version}
            </span>
          </h1>
        </div>
        <div className="flex flex-1 flex-wrap items-center justify-end gap-2">
          <Link
            to="/projects/$projectId/scenarios/$scenarioId/coverage"
            params={{ projectId, scenarioId: scenario.id }}
            className="inline-flex h-8 items-center gap-2 rounded-md border border-line bg-surface px-3 text-sm font-medium hover:border-line-strong hover:bg-surface-2"
          >
            <ListChecks className="size-3.5" aria-hidden /> API coverage
          </Link>
          <CliHint
            command={cli.start({ project: project.data?.slug, scenario: scenario.name })}
          >
            Run it
          </CliHint>
        </div>
      </div>
      {viewing != null && (
        <div className="flex items-center gap-3 border-b border-line bg-info-bg px-4 py-1.5 text-[13px]">
          <History className="size-4 text-info" aria-hidden />
          Viewing version {viewing}.
          <Button size="sm" variant="ghost" onClick={() => onView(undefined)}>
            Back to latest
          </Button>
        </div>
      )}
      <div className="flex items-center gap-2 border-b border-line bg-surface-2/40 px-4 py-1.5">
        <CliHint command={cli.push(project.data?.slug)} className="border-none px-0 py-0">
          Scenarios change from the terminal: edit the YAML and save a new version with
        </CliHint>
      </div>
      <div className="grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[minmax(0,1fr)_minmax(360px,42%)]">
        <div className="min-h-[50vh] border-r border-line bg-surface">
          {viewing != null && viewed.isPending ? (
            <Loading />
          ) : viewed.error ? (
            <ErrorAlert error={viewed.error} className="m-4" />
          ) : (
            <YamlView yaml={yaml} label={`${scenario.name} YAML`} className="h-full" />
          )}
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
                  problems.length ? 'bg-fail-bg text-fail' : 'bg-surface-2 text-muted',
                )}
              >
                {problems.length}
              </span>
            </Tabs.Trigger>
            <Tabs.Trigger value="history" className={tabTrigger}>
              History
            </Tabs.Trigger>
          </Tabs.List>
          <Tabs.Content value="journeys" className="min-h-[360px] flex-1">
            <JourneyGraphView yaml={yaml} />
          </Tabs.Content>
          <Tabs.Content value="plan" className="flex-1 overflow-y-auto">
            {validation.result?.valid === false && (
              <Notice tone="fail" className="m-3">
                The scenario has problems; see the Problems tab.
              </Notice>
            )}
            <PlanView plan={validation.result?.plan ?? version?.plan} />
          </Tabs.Content>
          <Tabs.Content value="problems" className="flex-1 overflow-y-auto">
            <ProblemsView
              problems={problems}
              validating={validation.pending}
              validationError={validation.error}
            />
          </Tabs.Content>
          <Tabs.Content value="history" className="flex-1 overflow-y-auto">
            <HistoryView scenario={scenario} viewing={viewing} onView={onView} />
          </Tabs.Content>
        </Tabs.Root>
      </div>
    </div>
  );
}

/** A saved scenario, read-only: its YAML, journey graph, plan and versions. */
export function ScenarioViewPage() {
  const { projectId, scenarioId } = useParams({
    from: '/app/projects/$projectId/scenarios/$scenarioId',
  });
  const search = useSearch({ from: '/app/projects/$projectId/scenarios/$scenarioId' });
  const navigate = useNavigate({ from: '/projects/$projectId/scenarios/$scenarioId' });
  const scenario = useScenario(scenarioId);

  if (scenario.isPending) return <Loading />;
  if (scenario.error) return <ErrorAlert error={scenario.error} className="m-6" />;
  return (
    <ScenarioLayout
      key={scenarioId}
      projectId={projectId}
      scenario={scenario.data}
      viewing={search.version}
      onView={(v) => void navigate({ search: v ? { version: v } : {} })}
    />
  );
}
