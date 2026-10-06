import { useNavigate } from '@tanstack/react-router';
import { ShieldAlert, ShieldCheck } from 'lucide-react';
import { useMemo, useState, type FormEvent } from 'react';
import {
  useCreateRun,
  useScenarios,
  useScenarioVersions,
  useTargets,
  useWorkers,
} from '@/api/queries';
import type { RunCreate, Target } from '@/api/types';
import { Modal } from '@/components/dialog';
import { Button, ErrorAlert, Field, Input, Notice, Select, Textarea } from '@/components/ui';
import { humanDuration, load, num } from '@/lib/format';
import { EnvFields, OverridesFields } from './RunFields';
import {
  envError,
  overrideErrors,
  overridesForm,
  toEnv,
  toOverrides,
  type EnvRow,
} from './runForm';

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
  const [ov, setOv] = useState(overridesForm);
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
    ...overrideErrors(ov),
    workers: !(Number.isInteger(Number(workerCount)) && Number(workerCount) >= 0)
      ? 'A whole number, 0 or more.'
      : undefined,
    env: envError(env),
  };
  const valid = Object.values(errors).every((e) => !e);
  const show = (k: keyof typeof errors) => (touched ? errors[k] : undefined);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (!valid) return;
    const overrides = toOverrides(ov);
    const envObj = toEnv(env);
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

        <OverridesFields
          id="run-overrides"
          ov={ov}
          setOv={setOv}
          errors={{ vus: show('vus'), rate: show('rate'), duration: show('duration') }}
        />

        <EnvFields env={env} setEnv={setEnv} error={show('env')} />

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
