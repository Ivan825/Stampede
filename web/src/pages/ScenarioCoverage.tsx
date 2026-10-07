import * as Tabs from '@radix-ui/react-tabs';
import { Link, useNavigate, useParams, useSearch } from '@tanstack/react-router';
import { clsx } from 'clsx';
import { CheckCircle2, XCircle } from 'lucide-react';
import { useId, useState, type FormEvent } from 'react';
import {
  useMe,
  useScenario,
  useScenarioCoverage,
  useScenarioDrift,
  useTargets,
} from '@/api/queries';
import type {
  DriftEndpoint,
  RequestRef,
  ScenarioCoverage,
  ScenarioDrift,
  Target,
} from '@/api/types';
import { Chip } from '@/components/chips';
import {
  Button,
  Card,
  CardHeader,
  ErrorAlert,
  Field,
  Input,
  Loading,
  Notice,
  PageHeader,
  Select,
  Stat,
  Table,
  Textarea,
} from '@/components/ui';
import { permissions } from '@/lib/roles';

export type CoverageTab = 'coverage' | 'drift';

const tabTrigger =
  'h-9 border-b-2 border-transparent px-1 text-[13px] text-muted hover:text-fg data-[state=active]:border-accent data-[state=active]:font-medium data-[state=active]:text-fg';

type Source = 'paste' | 'url';

/** Where an OpenAPI document comes from: pasted, or fetched from a target's host. */
interface SpecValue {
  source: Source;
  text: string;
  url: string;
}

const emptySpec: SpecValue = { source: 'paste', text: '', url: '' };

/** The server refuses larger documents. */
const maxSpecBytes = 5 << 20;

function specBody(v: SpecValue): { openapi?: string; specURL?: string } {
  if (v.source === 'url') return v.url.trim() ? { specURL: v.url.trim() } : {};
  return v.text.trim() ? { openapi: v.text } : {};
}

function SpecInput({
  label,
  value,
  onChange,
  targets,
  canFetch,
  optional,
}: {
  label: string;
  value: SpecValue;
  onChange: (v: SpecValue) => void;
  targets: Target[];
  canFetch: boolean;
  optional?: boolean;
}) {
  const name = useId();
  const [fileError, setFileError] = useState<string | null>(null);
  const hosts = [...new Set(targets.map((t) => new URL(t.baseURL).origin))];
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1 text-[13px] font-medium">
        {label}
        {optional && <span className="font-normal text-muted"> (optional)</span>}
      </legend>
      {canFetch && (
        <div className="flex gap-4 text-[13px]" role="radiogroup" aria-label={`${label} source`}>
          {(
            [
              ['paste', 'Paste the document'],
              ['url', 'Fetch from a target'],
            ] as const
          ).map(([s, text]) => (
            <label key={s} className="flex items-center gap-1.5">
              <input
                type="radio"
                name={name}
                checked={value.source === s}
                onChange={() => onChange({ ...value, source: s })}
              />
              {text}
            </label>
          ))}
        </div>
      )}
      {value.source === 'url' && canFetch ? (
        <Field
          label="Document URL"
          hint={
            hosts.length
              ? `On the host of a target in this project: ${hosts.join(', ')}`
              : 'Add a target first; the URL must be on a target’s host.'
          }
        >
          {(p) => (
            <Input
              {...p}
              placeholder={hosts[0] ? `${hosts[0]}/openapi.json` : 'https://…/openapi.json'}
              value={value.url}
              onChange={(e) => onChange({ ...value, url: e.target.value })}
            />
          )}
        </Field>
      ) : (
        <>
          <Field label="OpenAPI document (YAML or JSON)">
            {(p) => (
              <Textarea
                {...p}
                rows={8}
                spellCheck={false}
                className="font-mono text-xs"
                placeholder="openapi: 3.0.3&#10;paths:&#10;  /api/products:&#10;    get: …"
                value={value.text}
                onChange={(e) => onChange({ ...value, text: e.target.value })}
              />
            )}
          </Field>
          <label className="flex items-center gap-2 text-xs text-muted">
            Or load a file:
            <input
              type="file"
              accept=".yaml,.yml,.json,application/json,application/yaml,text/yaml"
              aria-label={`${label} file`}
              className="text-xs"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (!f) return;
                if (f.size > maxSpecBytes) {
                  setFileError(`${f.name} is larger than 5 MiB, the most the server accepts.`);
                  return;
                }
                setFileError(null);
                void f.text().then((text) => onChange({ ...value, source: 'paste', text }));
              }}
            />
          </label>
          {fileError && (
            <p role="alert" className="text-xs text-fail">
              {fileError}
            </p>
          )}
        </>
      )}
    </fieldset>
  );
}

function RequestsTable({ rows, caption }: { rows: RequestRef[]; caption: string }) {
  return (
    <Table aria-label={caption}>
      <thead>
        <tr>
          <th>Journey</th>
          <th>Method</th>
          <th>URL</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r, i) => (
          <tr key={i}>
            <td>{r.journey}</td>
            <td className="font-mono text-xs">{r.method}</td>
            <td className="font-mono text-xs break-all">{r.url}</td>
          </tr>
        ))}
      </tbody>
    </Table>
  );
}

export function CoverageResult({ c }: { c: ScenarioCoverage }) {
  const pct = c.total ? (c.covered / c.total) * 100 : 0;
  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-2 gap-2.5 md:grid-cols-4">
        <Stat label="endpoints covered" value={`${c.covered} of ${c.total}`} />
        <Stat label="coverage" value={`${pct.toFixed(0)}%`} tone={pct < 50 ? 'warn' : undefined} />
        <Stat
          label="requests matching no endpoint"
          value={c.unmatched.length}
          tone={c.unmatched.length ? 'fail' : undefined}
        />
        <Stat label="fully templated URLs" value={c.templated} />
      </div>
      <Card>
        <CardHeader
          title="Endpoints"
          description={`Checked against version ${c.version} of the scenario.`}
        />
        <Table aria-label="Endpoints">
          <thead>
            <tr>
              <th>Method</th>
              <th>Path</th>
              <th>Called by</th>
            </tr>
          </thead>
          <tbody>
            {c.endpoints.map((e) => (
              <tr
                key={`${e.method} ${e.path}`}
                className={e.journeys.length ? undefined : 'bg-warn-bg/40'}
              >
                <td className="font-mono text-xs">{e.method}</td>
                <td>
                  <span className="font-mono text-xs">{e.path}</span>
                  {e.summary && <span className="ml-2 text-xs text-muted">{e.summary}</span>}
                </td>
                <td>
                  {e.journeys.length ? (
                    <span className="flex flex-wrap gap-1">
                      {e.journeys.map((j) => (
                        <Chip key={j} tone="pass">
                          {j}
                        </Chip>
                      ))}
                    </span>
                  ) : (
                    <span className="text-xs text-muted">no journey</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      </Card>
      {c.unmatched.length > 0 && (
        <Card>
          <CardHeader
            title="Requests that match no endpoint"
            description="Often a typo or an endpoint that was renamed."
          />
          <RequestsTable rows={c.unmatched} caption="Requests that match no endpoint" />
        </Card>
      )}
    </div>
  );
}

const endpointText = (e: DriftEndpoint) => `${e.method} ${e.path}`;

export function DriftResult({ d }: { d: ScenarioDrift }) {
  return (
    <div className="flex flex-col gap-4">
      <div
        role="status"
        className={clsx(
          'flex items-center gap-2 rounded-lg border px-4 py-3 text-[13px]',
          d.drifted ? 'border-fail/50 bg-fail-bg' : 'border-pass/50 bg-pass-bg',
        )}
      >
        {d.drifted ? (
          <XCircle className="size-4 text-fail" aria-hidden />
        ) : (
          <CheckCircle2 className="size-4 text-pass" aria-hidden />
        )}
        <span className="font-medium">
          {d.drifted
            ? 'The scenario drifted from the API.'
            : 'No drift: every request uses an endpoint of the current API.'}
        </span>
        <span className="ml-auto text-xs text-muted">version {d.version}</span>
      </div>
      {d.removed && (
        <Card>
          <CardHeader
            title="API change"
            description={`${d.added?.length ?? 0} endpoints added, ${d.removed.length} removed`}
          />
          <ul className="flex flex-col gap-1 px-4 py-3 font-mono text-xs" aria-label="API change">
            {d.removed.map((e) => (
              <li key={`-${endpointText(e)}`} className="text-fail">
                − {endpointText(e)}
              </li>
            ))}
            {(d.added ?? []).map((e) => (
              <li key={`+${endpointText(e)}`} className="text-pass">
                + {endpointText(e)}
              </li>
            ))}
            {d.removed.length + (d.added?.length ?? 0) === 0 && (
              <li className="font-sans text-muted">No endpoints added or removed.</li>
            )}
          </ul>
        </Card>
      )}
      {(d.broken?.length ?? 0) > 0 && (
        <Card>
          <CardHeader title="Journeys that call removed endpoints" />
          <Table aria-label="Broken journeys">
            <tbody>
              {d.broken!.map((b) => (
                <tr key={b.journey}>
                  <td className="w-48 font-medium">{b.journey}</td>
                  <td className="font-mono text-xs">{b.endpoints.map(endpointText).join(', ')}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
      {d.unmatched.length > 0 && (
        <Card>
          <CardHeader
            title="Requests that use no endpoint of the current API"
            description="Often a typo or an endpoint that was renamed."
          />
          <RequestsTable rows={d.unmatched} caption="Requests that use no endpoint" />
        </Card>
      )}
      {d.dryRun && (
        <Card>
          <CardHeader
            title="Dry run"
            description="Every journey once with one user against the target."
          />
          <Table aria-label="Dry run">
            <tbody>
              {d.dryRun.map((c) => (
                <tr key={c.journey}>
                  <td className="w-48 font-medium">{c.journey}</td>
                  <td>
                    <Chip tone={c.ok ? 'pass' : 'fail'}>{c.ok ? 'passes' : 'fails'}</Chip>
                  </td>
                  <td className="text-xs">
                    {!c.ok && (
                      <>
                        {c.step && <span className="font-mono">step {c.step}: </span>}
                        {c.error ?? (c.status ? `status ${c.status}` : 'failed')}
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
    </div>
  );
}

function CoverageForm({
  scenarioId,
  targets,
  canFetch,
}: {
  scenarioId: string;
  targets: Target[];
  canFetch: boolean;
}) {
  const [spec, setSpec] = useState<SpecValue>(emptySpec);
  const coverage = useScenarioCoverage(scenarioId);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    coverage.mutate(specBody(spec));
  };
  return (
    <div className="flex flex-col gap-4">
      <Card className="px-4 py-3">
        <form className="flex flex-col gap-3" onSubmit={submit} aria-label="Check coverage">
          <p className="text-[13px] text-muted">
            Which endpoints of the API the scenario’s journeys call, and which none does. No
            requests are sent to the target.
          </p>
          <SpecInput
            label="The API"
            value={spec}
            onChange={setSpec}
            targets={targets}
            canFetch={canFetch}
          />
          <div>
            <Button type="submit" variant="primary" loading={coverage.isPending}>
              Check coverage
            </Button>
          </div>
          <ErrorAlert error={coverage.error} />
        </form>
      </Card>
      {coverage.data && <CoverageResult c={coverage.data} />}
    </div>
  );
}

function DriftForm({
  scenarioId,
  targets,
  canFetch,
  canDryRun,
}: {
  scenarioId: string;
  targets: Target[];
  canFetch: boolean;
  canDryRun: boolean;
}) {
  const [cur, setCur] = useState<SpecValue>(emptySpec);
  const [prev, setPrev] = useState<SpecValue>(emptySpec);
  const [targetId, setTargetId] = useState('');
  const drift = useScenarioDrift(scenarioId);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const p = specBody(prev);
    drift.mutate({
      ...specBody(cur),
      ...(p.openapi ? { previousOpenapi: p.openapi } : {}),
      ...(p.specURL ? { previousSpecURL: p.specURL } : {}),
      ...(targetId ? { targetId } : {}),
    });
  };
  return (
    <div className="flex flex-col gap-4">
      <Card className="px-4 py-3">
        <form className="flex flex-col gap-4" onSubmit={submit} aria-label="Check drift">
          <p className="text-[13px] text-muted">
            Find journeys an API change broke: endpoints removed since the previous version,
            requests the current API no longer serves and, with a target, journeys that now fail a
            dry run.
          </p>
          <SpecInput
            label="Current API"
            value={cur}
            onChange={setCur}
            targets={targets}
            canFetch={canFetch}
          />
          <SpecInput
            label="Previous API"
            value={prev}
            onChange={setPrev}
            targets={targets}
            canFetch={canFetch}
            optional
          />
          {canDryRun && (
            <Field
              label="Dry run against"
              hint="Runs every journey once with one user. This sends real requests to the target."
            >
              {(p) => (
                <Select {...p} value={targetId} onChange={(e) => setTargetId(e.target.value)}>
                  <option value="">No dry run</option>
                  {targets.map((t) => (
                    <option key={t.id} value={t.id}>
                      {t.name} ({t.baseURL})
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          )}
          <div>
            <Button type="submit" variant="primary" loading={drift.isPending}>
              Check drift
            </Button>
          </div>
          <ErrorAlert error={drift.error} />
        </form>
      </Card>
      {drift.data && <DriftResult d={drift.data} />}
    </div>
  );
}

/** API coverage and drift of a saved scenario. */
export function ScenarioCoveragePage() {
  const { projectId, scenarioId } = useParams({
    from: '/app/projects/$projectId/scenarios/$scenarioId/coverage',
  });
  const search = useSearch({ from: '/app/projects/$projectId/scenarios/$scenarioId/coverage' });
  const navigate = useNavigate({ from: '/projects/$projectId/scenarios/$scenarioId/coverage' });
  const me = useMe();
  const can = permissions(me.role);
  const scenario = useScenario(scenarioId);
  const targets = useTargets(projectId);
  if (scenario.isPending) return <Loading />;
  if (scenario.error) return <ErrorAlert error={scenario.error} className="m-6" />;
  const tab = search.tab ?? 'coverage';
  const list = targets.data ?? [];
  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        breadcrumb={
          <>
            <Link
              to="/projects/$projectId/scenarios"
              params={{ projectId }}
              className="hover:underline"
            >
              Scenarios
            </Link>{' '}
            /{' '}
            <Link
              to="/projects/$projectId/scenarios/$scenarioId"
              params={{ projectId, scenarioId }}
              className="hover:underline"
            >
              {scenario.data.name}
            </Link>
          </>
        }
        title="API coverage and drift"
        description="The same checks as stampede coverage and stampede drift."
      />
      {!can.editScenarios && (
        <Notice tone="info" className="mb-4">
          Paste the OpenAPI document; fetching it from a target needs the editor role.
        </Notice>
      )}
      <Tabs.Root
        value={tab}
        onValueChange={(v) => void navigate({ search: { tab: v as CoverageTab } })}
      >
        <Tabs.List className="mb-4 flex gap-5 border-b border-line" aria-label="Checks">
          <Tabs.Trigger value="coverage" className={tabTrigger}>
            Coverage
          </Tabs.Trigger>
          <Tabs.Trigger value="drift" className={tabTrigger}>
            Drift
          </Tabs.Trigger>
        </Tabs.List>
        <Tabs.Content value="coverage">
          <CoverageForm scenarioId={scenarioId} targets={list} canFetch={can.editScenarios} />
        </Tabs.Content>
        <Tabs.Content value="drift">
          <DriftForm
            scenarioId={scenarioId}
            targets={list}
            canFetch={can.editScenarios}
            canDryRun={can.startRuns}
          />
        </Tabs.Content>
      </Tabs.Root>
    </div>
  );
}
