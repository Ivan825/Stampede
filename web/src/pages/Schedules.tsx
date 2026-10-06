import { Link, useNavigate, useParams } from '@tanstack/react-router';
import { clsx } from 'clsx';
import { Pencil, Play, Plus, Trash2 } from 'lucide-react';
import { useEffect, useState, type FormEvent } from 'react';
import {
  useCreateSchedule,
  useDeleteSchedule,
  useMe,
  useRunSchedule,
  useScenarios,
  useSchedulePreview,
  useSchedules,
  useTargets,
  useUpdateSchedule,
} from '@/api/queries';
import type { Schedule, ScheduleCreate, Verdict } from '@/api/types';
import { isActive } from '@/api/types';
import { Chip, StatusChip, VerdictChip } from '@/components/chips';
import { Confirm, Modal } from '@/components/dialog';
import { useToast } from '@/components/toast';
import {
  Button,
  Card,
  EmptyState,
  ErrorAlert,
  Field,
  Input,
  Loading,
  PageHeader,
  Select,
  Table,
  Textarea,
} from '@/components/ui';
import { CapsSummary } from '@/features/runs/NewRunDialog';
import { EnvFields, OverridesFields } from '@/features/runs/RunFields';
import {
  envError,
  envRows,
  overrideErrors,
  overridesForm,
  toEnv,
  toOverrides,
} from '@/features/runs/runForm';
import { dateTime, relativeTime } from '@/lib/format';
import { permissions } from '@/lib/roles';

const presets: { label: string; cron: string }[] = [
  { label: 'Hourly', cron: '0 * * * *' },
  { label: 'Daily 02:00', cron: '0 2 * * *' },
  { label: 'Weekdays 06:30', cron: '30 6 * * MON-FRI' },
  { label: 'Mondays 03:00', cron: '0 3 * * MON' },
];

/** A firing time in the schedule's own zone: "Thu 8 Oct, 02:00 BST". */
function zoned(iso: string, timeZone: string): string {
  try {
    return new Intl.DateTimeFormat('en-GB', {
      weekday: 'short',
      day: 'numeric',
      month: 'short',
      hour: '2-digit',
      minute: '2-digit',
      timeZone,
      timeZoneName: 'short',
    }).format(new Date(iso));
  } catch {
    return dateTime(iso);
  }
}

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

const zones: string[] = (() => {
  try {
    return Intl.supportedValuesOf('timeZone');
  } catch {
    return [];
  }
})();

function CronPreview({ cron, timezone }: { cron: string; timezone: string }) {
  const c = useDebounced(cron.trim(), 300);
  const tz = useDebounced(timezone.trim(), 300);
  const preview = useSchedulePreview(c, tz);
  if (!c) return null;
  if (preview.error) {
    return (
      <p className="text-xs text-fail" role="status">
        {preview.error.message}
      </p>
    );
  }
  if (!preview.data) return <p className="text-xs text-muted">Checking…</p>;
  return (
    <div className="text-xs" role="status" aria-label="Next runs">
      <span className="text-muted">Next runs: </span>
      {preview.data.next.map((t, i) => (
        <span key={t} className="num">
          {i > 0 && <span className="text-muted"> · </span>}
          {zoned(t, preview.data.timezone)}
        </span>
      ))}
    </div>
  );
}

function ScheduleDialog({
  projectId,
  schedule,
  open,
  onOpenChange,
}: {
  projectId: string;
  schedule?: Schedule;
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const scenarios = useScenarios(projectId);
  const targets = useTargets(projectId);
  const create = useCreateSchedule(projectId);
  const update = useUpdateSchedule(projectId);
  const m = schedule ? update : create;

  const [name, setName] = useState(schedule?.name ?? '');
  const [scenarioChoice, setScenarioId] = useState(schedule?.scenarioId ?? '');
  const [targetChoice, setTargetId] = useState(schedule?.targetId ?? '');
  const [cron, setCron] = useState(schedule?.cron ?? '0 2 * * *');
  const [timezone, setTimezone] = useState(schedule?.timezone ?? 'UTC');
  const [ov, setOv] = useState(() => overridesForm(schedule?.overrides));
  const [env, setEnv] = useState(() => envRows(schedule?.env));
  const [workerCount, setWorkerCount] = useState(String(schedule?.workers ?? 0));
  const [note, setNote] = useState(schedule?.note ?? '');
  const [enabled, setEnabled] = useState(schedule?.enabled ?? true);
  const [touched, setTouched] = useState(false);

  const scenarioId = scenarioChoice || scenarios.data?.[0]?.id || '';
  const targetId = targetChoice || targets.data?.[0]?.id || '';
  const target = targets.data?.find((t) => t.id === targetId);

  const errors = {
    name: name.trim() ? undefined : 'Enter a name.',
    scenario: scenarioId ? undefined : 'Pick a scenario.',
    target: targetId ? undefined : 'Pick a target.',
    cron: cron.trim() ? undefined : 'Enter a cron expression.',
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
    const body: ScheduleCreate = {
      name: name.trim(),
      scenarioId,
      targetId,
      cron: cron.trim(),
      timezone: timezone.trim() || 'UTC',
      overrides: toOverrides(ov),
      env: toEnv(env),
      workers: Number(workerCount),
      enabled,
      note: note.trim(),
    };
    const opts = { onSuccess: () => onOpenChange(false) };
    if (schedule) update.mutate({ id: schedule.id, ...body }, opts);
    else create.mutate(body, opts);
  };

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={schedule ? `Edit ${schedule.name}` : 'New schedule'}
      description="Start a run of a scenario on a cron schedule. The server checks the run now, so a schedule it accepts would start if it fired."
      width="max-w-2xl"
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button variant="primary" type="submit" form="schedule-form" loading={m.isPending}>
            {schedule ? 'Save' : 'Create schedule'}
          </Button>
        </>
      }
    >
      <form id="schedule-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <Field label="Name" error={show('name')}>
          {(p) => (
            <Input
              {...p}
              autoFocus
              maxLength={100}
              placeholder="nightly-checkout"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          )}
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Scenario" hint="Runs the latest version." error={show('scenario')}>
            {(p) => (
              <Select {...p} value={scenarioId} onChange={(e) => setScenarioId(e.target.value)}>
                {!scenarios.data?.length && <option value="">No scenarios</option>}
                {scenarios.data?.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
            )}
          </Field>
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
        </div>
        {target && <CapsSummary target={target} />}

        <div className="grid grid-cols-[1fr_14rem] gap-3">
          <Field
            label="Cron"
            hint="minute hour day-of-month month day-of-week, or @hourly, @daily, @weekly."
            error={show('cron')}
          >
            {(p) => (
              <Input
                {...p}
                className="font-mono"
                placeholder="0 2 * * *"
                value={cron}
                onChange={(e) => setCron(e.target.value)}
              />
            )}
          </Field>
          <Field label="Time zone" hint="IANA name; UTC by default.">
            {(p) => (
              <Input
                {...p}
                list="schedule-zones"
                placeholder="UTC"
                value={timezone}
                onChange={(e) => setTimezone(e.target.value)}
              />
            )}
          </Field>
          <datalist id="schedule-zones">
            {zones.map((z) => (
              <option key={z} value={z} />
            ))}
          </datalist>
        </div>
        <div className="-mt-2 flex flex-col gap-2">
          <div className="flex flex-wrap gap-1.5">
            {presets.map((pr) => (
              <Button key={pr.cron} size="sm" variant="ghost" onClick={() => setCron(pr.cron)}>
                {pr.label}
              </Button>
            ))}
          </div>
          <CronPreview cron={cron} timezone={timezone} />
        </div>

        <OverridesFields
          id="schedule-overrides"
          ov={ov}
          setOv={setOv}
          errors={{ vus: show('vus'), rate: show('rate'), duration: show('duration') }}
          defaultOpen={Object.keys(schedule?.overrides ?? {}).length > 0}
        />
        <EnvFields
          env={env}
          setEnv={setEnv}
          error={show('env')}
          help="They are stored with the schedule and anyone in the project can read them, so use Secrets for credentials."
        />

        <div className="grid grid-cols-[9rem_1fr] gap-3">
          <Field label="Workers" hint="0 uses every connected worker." error={show('workers')}>
            {(p) => (
              <Input
                {...p}
                inputMode="numeric"
                value={workerCount}
                onChange={(e) => setWorkerCount(e.target.value)}
              />
            )}
          </Field>
          <Field label="Note" hint="Optional. What the schedule is for.">
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
        <label className="flex items-center gap-2 text-[13px]">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          Enabled
        </label>
        <p className="-mt-2 text-xs text-muted">
          Runs start as you. Saving a schedule makes you its owner.
        </p>
        <ErrorAlert error={m.error} />
      </form>
    </Modal>
  );
}

function Switch({
  checked,
  onChange,
  label,
  disabled,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={clsx(
        'relative inline-flex h-5 w-9 shrink-0 items-center rounded-full border transition-colors disabled:opacity-50',
        checked ? 'border-primary bg-primary' : 'border-line bg-surface-2',
      )}
    >
      <span
        className={clsx(
          'inline-block size-3.5 rounded-full bg-white shadow transition-transform',
          checked ? 'translate-x-4' : 'translate-x-0.5',
        )}
      />
    </button>
  );
}

function LastRun({ s }: { s: Schedule }) {
  if (s.lastSkipReason) {
    return (
      <div className="flex flex-col gap-0.5">
        <Chip tone="warn" title={s.lastSkipReason}>
          SKIPPED
        </Chip>
        <span className="max-w-64 text-xs text-muted">{s.lastSkipReason}</span>
      </div>
    );
  }
  if (!s.lastRunId) return <span className="text-muted">Never</span>;
  return (
    <Link
      to="/runs/$runId"
      params={{ runId: s.lastRunId }}
      className="flex items-center gap-2 hover:underline"
    >
      {s.lastRunStatus && isActive(s.lastRunStatus) ? (
        <StatusChip status={s.lastRunStatus} />
      ) : s.lastRunVerdict ? (
        <VerdictChip verdict={s.lastRunVerdict as Verdict} />
      ) : (
        s.lastRunStatus && <StatusChip status={s.lastRunStatus} />
      )}
      <span className="text-xs text-muted">{relativeTime(s.lastRunAt)}</span>
    </Link>
  );
}

export function SchedulesPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/schedules' });
  const me = useMe();
  const can = permissions(me.role);
  const schedules = useSchedules(projectId);
  const update = useUpdateSchedule(projectId);
  const del = useDeleteSchedule(projectId);
  const runNow = useRunSchedule(projectId);
  const toast = useToast();
  const navigate = useNavigate();
  const [editing, setEditing] = useState<Schedule | 'new' | null>(null);

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        title="Schedules"
        description="Start runs on a cron schedule, such as a nightly regression check. Times are UTC unless a schedule names a time zone."
        actions={
          can.editSchedules && (
            <Button variant="primary" onClick={() => setEditing('new')}>
              <Plus className="size-4" aria-hidden /> New schedule
            </Button>
          )
        }
      />
      <Card>
        {schedules.isPending ? (
          <Loading />
        ) : schedules.error ? (
          <ErrorAlert error={schedules.error} className="m-4" />
        ) : schedules.data.length === 0 ? (
          <EmptyState title="No schedules">
            A schedule runs a scenario against a target at set times, for example every night at
            02:00.
          </EmptyState>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>Name</th>
                <th>When</th>
                <th>Next run</th>
                <th>Last run</th>
                <th>Enabled</th>
                <th className="sr-only">Actions</th>
              </tr>
            </thead>
            <tbody>
              {schedules.data.map((s) => (
                <tr key={s.id}>
                  <td className="max-w-72">
                    <div className="font-medium">{s.name}</div>
                    <div className="truncate text-xs text-muted">
                      {s.scenarioName} → {s.targetName}
                    </div>
                    {s.note && <div className="truncate text-xs text-muted">{s.note}</div>}
                  </td>
                  <td>
                    <div className="font-mono text-xs">{s.cron}</div>
                    <div className="text-xs text-muted">{s.timezone}</div>
                  </td>
                  <td>
                    {s.nextRunAt ? (
                      <>
                        <div className="num">{zoned(s.nextRunAt, s.timezone)}</div>
                        <div className="text-xs text-muted">{relativeTime(s.nextRunAt)}</div>
                      </>
                    ) : (
                      <span className="text-muted">Disabled</span>
                    )}
                  </td>
                  <td>
                    <LastRun s={s} />
                  </td>
                  <td>
                    {can.editSchedules ? (
                      <Switch
                        checked={s.enabled}
                        label={`${s.enabled ? 'Disable' : 'Enable'} ${s.name}`}
                        disabled={update.isPending}
                        onChange={(v) =>
                          update.mutate(
                            { id: s.id, enabled: v },
                            {
                              onSuccess: () =>
                                toast.success(`${v ? 'Enabled' : 'Disabled'} ${s.name}.`),
                              onError: (e) => toast.error(e),
                            },
                          )
                        }
                      />
                    ) : (
                      <span className="text-muted">{s.enabled ? 'On' : 'Off'}</span>
                    )}
                  </td>
                  <td>
                    <div className="flex items-center justify-end gap-2">
                      {can.startRuns && (
                        <Button
                          size="sm"
                          aria-label={`Run ${s.name} now`}
                          loading={runNow.isPending && runNow.variables === s.id}
                          onClick={() =>
                            runNow.mutate(s.id, {
                              onSuccess: (run) =>
                                void navigate({ to: '/runs/$runId', params: { runId: run.id } }),
                              onError: (e) => toast.error(e),
                            })
                          }
                        >
                          <Play className="size-3.5" aria-hidden /> Run now
                        </Button>
                      )}
                      {can.editSchedules && (
                        <>
                          <Button
                            size="sm"
                            onClick={() => setEditing(s)}
                            aria-label={`Edit ${s.name}`}
                          >
                            <Pencil className="size-3.5" aria-hidden />
                          </Button>
                          <Confirm
                            trigger={
                              <Button size="sm" variant="danger" aria-label={`Delete ${s.name}`}>
                                <Trash2 className="size-3.5" aria-hidden />
                              </Button>
                            }
                            title={`Delete ${s.name}?`}
                            description="It stops firing. Runs it already started keep their results."
                            confirmLabel="Delete schedule"
                            destructive
                            onConfirm={async () => {
                              await del.mutateAsync(s.id);
                              toast.success(`Deleted ${s.name}.`);
                            }}
                          />
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      {editing && (
        <ScheduleDialog
          key={editing === 'new' ? 'new' : editing.id}
          projectId={projectId}
          schedule={editing === 'new' ? undefined : editing}
          open
          onOpenChange={(v) => !v && setEditing(null)}
        />
      )}
    </div>
  );
}
