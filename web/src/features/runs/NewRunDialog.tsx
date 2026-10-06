import { useNavigate } from '@tanstack/react-router';
import { ChevronDown, ChevronRight, Plus, ShieldAlert, ShieldCheck, X } from 'lucide-react';
import { useMemo, useState, type FormEvent } from 'react';
import {
  useCreateRun,
  useScenarios,
  useScenarioVersions,
  useTargets,
  useWorkers,
} from '@/api/queries';
import type { RunCreate, RunOverrides, Target } from '@/api/types';
import { shapes } from '@/api/types';
import { Modal } from '@/components/dialog';
import { Button, ErrorAlert, Field, Input, Notice, Select, Textarea } from '@/components/ui';
import { humanDuration, load, num } from '@/lib/format';

const ratePattern = /^[0-9.]+(\/(s|sec|second|m|min|minute|h|hour))?$/;
const durationPattern = /^([0-9.]+(ns|us|µs|ms|s|m|h|d)?)+$/;

export function CapsSummary({ target }: { target: Target }) {
  const c = target.caps ?? {};
  const capList = [
    c.maxRate != null && `${num(c.maxRate)} iterations/s`,
    c.maxVUs != null && `${c.maxVUs} VUs`,
    c.maxDurationSeconds != null && `${humanDuration(c.maxDurationSeconds)} per run`,
  ].filter(Boolean) as string[];
  const caps = capList.length ? capList.join(' · ') : 'none set';

  if (target.private) {
    return (
      <Notice tone="info">
        <div className="flex items-start gap-2">
          <ShieldCheck className="mt-0.5 size-4 shrink-0 text-info" aria-hidden />
          <div>
            <p className="font-medium">Private target — no verification needed.</p>
            <p className="text-muted">Caps: {caps}</p>
          </div>
        </div>
      </Notice>
    );
  }
  if (target.verified) {
    return (
      <Notice tone="pass">
        <div className="flex items-start gap-2">
          <ShieldCheck className="mt-0.5 size-4 shrink-0 text-pass" aria-hidden />
          <div>
            <p className="font-medium">Verified public target.</p>
            <p className="text-muted">Caps: {caps}</p>
          </div>
        </div>
      </Notice>
    );
  }
  return (
    <Notice tone="warn">
      <div className="flex items-start gap-2">
        <ShieldAlert className="mt-0.5 size-4 shrink-0 text-warn" aria-hidden />
        <div>
          <p className="font-medium">Unverified public target — low safety caps apply.</p>
          <p className="text-muted">
            Caps: {caps}. Verify ownership on the Targets page to lift them. The server lowers any
            load above the caps.
          </p>
        </div>
      </div>
    </Notice>
  );
}

interface EnvRow {
  key: string;
  value: string;
}

export function NewRunDialog({
  projectId,
  open,
  onOpenChange,
  scenarioId: initialScenario,
}: {
  projectId: string;
  open: boolean;
  onOpenChange: (v: boolean) => void;
  scenarioId?: string;
}) {
  const navigate = useNavigate();
  const scenarios = useScenarios(projectId);
  const targets = useTargets(projectId);
  const workers = useWorkers();
  const create = useCreateRun(projectId);

  const [scenarioChoice, setScenarioId] = useState('');
  const [version, setVersion] = useState('');
  const [targetChoice, setTargetId] = useState('');
  const [showOverrides, setShowOverrides] = useState(false);
  const [ov, setOv] = useState<Record<keyof RunOverrides, string>>({
    shape: '',
    mode: '',
    vus: '',
    rate: '',
    duration: '',
    start: '',
    max: '',
  });
  const [env, setEnv] = useState<EnvRow[]>([]);
  const [workerCount, setWorkerCount] = useState('0');
  const [note, setNote] = useState('');
  const [touched, setTouched] = useState(false);

  // Explicit choices win; otherwise the given scenario, then the first one.
  const scenarioId = scenarioChoice || initialScenario || scenarios.data?.[0]?.id || '';
  const targetId = targetChoice || targets.data?.[0]?.id || '';

  const versions = useScenarioVersions(scenarioId, open && !!scenarioId);
  const scenario = scenarios.data?.find((s) => s.id === scenarioId);
  const target = targets.data?.find((t) => t.id === targetId);
  const plan = useMemo(() => {
    if (!version) return scenario?.latestVersion.plan;
    return versions.data?.find((v) => String(v.version) === version)?.plan;
  }, [scenario, versions.data, version]);

  const connected = (workers.data ?? []).filter((w) => w.status !== 'lost').length;

  const errors = {
    scenario: scenarioId ? undefined : 'Pick a scenario.',
    target: targetId ? undefined : 'Pick a target.',
    vus:
      ov.vus && !(Number.isInteger(Number(ov.vus)) && Number(ov.vus) >= 1)
        ? 'A whole number, at least 1.'
        : undefined,
    rate: ov.rate && !ratePattern.test(ov.rate.trim()) ? 'For example 50/s or 3000/m.' : undefined,
    duration:
      ov.duration && !durationPattern.test(ov.duration.trim())
        ? 'For example 30s, 5m or 1h30m.'
        : undefined,
    workers: !(Number.isInteger(Number(workerCount)) && Number(workerCount) >= 0)
      ? 'A whole number, 0 or more.'
      : undefined,
    env: env.some((r) => r.key && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(r.key))
      ? 'Variable names use letters, digits and underscores.'
      : undefined,
  };
  const valid = Object.values(errors).every((e) => !e);
  const show = (k: keyof typeof errors) => (touched ? errors[k] : undefined);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (!valid) return;
    const overrides: RunOverrides = {};
    if (ov.shape) overrides.shape = ov.shape;
    if (ov.mode === 'vus' || ov.mode === 'rate') overrides.mode = ov.mode;
    if (ov.vus) overrides.vus = Number(ov.vus);
    if (ov.rate.trim()) overrides.rate = ov.rate.trim();
    if (ov.duration.trim()) overrides.duration = ov.duration.trim();
    if (ov.start.trim()) overrides.start = ov.start.trim();
    if (ov.max.trim()) overrides.max = ov.max.trim();
    const envObj = Object.fromEntries(env.filter((r) => r.key).map((r) => [r.key, r.value]));
    const body: RunCreate = {
      scenarioId,
      targetId,
      workers: Number(workerCount),
      ...(version ? { version: Number(version) } : {}),
      ...(Object.keys(overrides).length ? { overrides } : {}),
      ...(Object.keys(envObj).length ? { env: envObj } : {}),
      ...(note.trim() ? { note: note.trim() } : {}),
    };
    create.mutate(body, {
      onSuccess: (run) => {
        onOpenChange(false);
        void navigate({ to: '/runs/$runId', params: { runId: run.id } });
      },
    });
  };

  const setO = (k: keyof RunOverrides) => (e: { target: { value: string } }) =>
    setOv((o) => ({ ...o, [k]: e.target.value }));

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="New run"
      description="Run a scenario against a target. Load overrides apply to this run only."
      width="max-w-2xl"
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button variant="primary" type="submit" form="new-run" loading={create.isPending}>
            Start run
          </Button>
        </>
      }
    >
      <form id="new-run" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <div className="grid grid-cols-[1fr_9rem] gap-3">
          <Field label="Scenario" error={show('scenario')}>
            {(p) => (
              <Select
                {...p}
                value={scenarioId}
                onChange={(e) => {
                  setScenarioId(e.target.value);
                  setVersion('');
                }}
              >
                {!scenarios.data?.length && <option value="">No scenarios</option>}
                {scenarios.data?.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label="Version">
            {(p) => (
              <Select {...p} value={version} onChange={(e) => setVersion(e.target.value)}>
                <option value="">
                  Latest{scenario ? ` (v${scenario.latestVersion.version})` : ''}
                </option>
                {versions.data?.map((v) => (
                  <option key={v.version} value={v.version}>
                    v{v.version}
                    {v.message ? ` — ${v.message.slice(0, 40)}` : ''}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>

        {plan && (
          <p className="num -mt-2 text-xs text-muted">
            Plan: {plan.executor}
            {plan.shape ? ` · ${plan.shape}` : ''} · peak {load(plan.peak, plan.mode)} ·{' '}
            {humanDuration(plan.durationSeconds)} · {plan.journeys} journeys, {plan.steps} steps
          </p>
        )}

        <Field label="Target" error={show('target')}>
          {(p) => (
            <Select {...p} value={targetId} onChange={(e) => setTargetId(e.target.value)}>
              {!targets.data?.length && <option value="">No targets</option>}
              {targets.data?.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name} — {t.baseURL}
                </option>
              ))}
            </Select>
          )}
        </Field>
        {target && <CapsSummary target={target} />}

        <div className="rounded-md border border-line">
          <button
            type="button"
            className="flex w-full items-center gap-1.5 px-3 py-2 text-left text-[13px] font-medium"
            aria-expanded={showOverrides}
            aria-controls="run-overrides"
            onClick={() => setShowOverrides((v) => !v)}
          >
            {showOverrides ? (
              <ChevronDown className="size-4" aria-hidden />
            ) : (
              <ChevronRight className="size-4" aria-hidden />
            )}
            Load overrides
            <span className="font-normal text-muted">— optional</span>
          </button>
          {showOverrides && (
            <div id="run-overrides" className="grid grid-cols-3 gap-3 border-t border-line p-3">
              <Field label="Shape">
                {(p) => (
                  <Select {...p} value={ov.shape} onChange={setO('shape')}>
                    <option value="">From scenario</option>
                    {shapes.map((s) => (
                      <option key={s} value={s}>
                        {s}
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
              <Field label="Mode">
                {(p) => (
                  <Select {...p} value={ov.mode} onChange={setO('mode')}>
                    <option value="">From scenario</option>
                    <option value="rate">rate (open model)</option>
                    <option value="vus">vus (closed model)</option>
                  </Select>
                )}
              </Field>
              <Field label="Duration" error={show('duration')}>
                {(p) => (
                  <Input {...p} placeholder="5m" value={ov.duration} onChange={setO('duration')} />
                )}
              </Field>
              <Field label="Virtual users" error={show('vus')}>
                {(p) => (
                  <Input
                    {...p}
                    inputMode="numeric"
                    placeholder="50"
                    value={ov.vus}
                    onChange={setO('vus')}
                  />
                )}
              </Field>
              <Field label="Rate" error={show('rate')}>
                {(p) => (
                  <Input {...p} placeholder="100/s" value={ov.rate} onChange={setO('rate')} />
                )}
              </Field>
              <div />
              <Field label="Start level" hint="Shapes only.">
                {(p) => (
                  <Input {...p} placeholder="10/s" value={ov.start} onChange={setO('start')} />
                )}
              </Field>
              <Field label="Max level" hint="Shapes only.">
                {(p) => <Input {...p} placeholder="500/s" value={ov.max} onChange={setO('max')} />}
              </Field>
            </div>
          )}
        </div>

        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1 text-[13px] font-medium">Environment</legend>
          {env.length === 0 && (
            <p className="text-xs text-muted">
              Values for <code className="font-mono">{'${env.NAME}'}</code> in the scenario. Use
              Secrets for credentials.
            </p>
          )}
          {env.map((row, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input
                aria-label={`Variable ${i + 1} name`}
                placeholder="NAME"
                className="font-mono"
                value={row.key}
                onChange={(e) =>
                  setEnv((rows) =>
                    rows.map((r, j) => (j === i ? { ...r, key: e.target.value } : r)),
                  )
                }
              />
              <Input
                aria-label={`Variable ${i + 1} value`}
                placeholder="value"
                className="font-mono"
                value={row.value}
                onChange={(e) =>
                  setEnv((rows) =>
                    rows.map((r, j) => (j === i ? { ...r, value: e.target.value } : r)),
                  )
                }
              />
              <Button
                variant="ghost"
                size="sm"
                aria-label={`Remove variable ${i + 1}`}
                onClick={() => setEnv((rows) => rows.filter((_, j) => j !== i))}
              >
                <X className="size-3.5" aria-hidden />
              </Button>
            </div>
          ))}
          {show('env') && <p className="text-xs text-fail">{show('env')}</p>}
          <div>
            <Button size="sm" onClick={() => setEnv((rows) => [...rows, { key: '', value: '' }])}>
              <Plus className="size-3.5" aria-hidden /> Add variable
            </Button>
          </div>
        </fieldset>

        <div className="grid grid-cols-[9rem_1fr] gap-3">
          <Field
            label="Workers"
            hint={`0 uses every connected worker (${connected} now).`}
            error={show('workers')}
          >
            {(p) => (
              <Input
                {...p}
                inputMode="numeric"
                value={workerCount}
                onChange={(e) => setWorkerCount(e.target.value)}
              />
            )}
          </Field>
          <Field label="Note" hint="Optional. Shown in the runs list.">
            {(p) => (
              <Textarea
                {...p}
                rows={1}
                maxLength={500}
                value={note}
                onChange={(e) => setNote(e.target.value)}
              />
            )}
          </Field>
        </div>

        <ErrorAlert error={create.error} />
      </form>
    </Modal>
  );
}
