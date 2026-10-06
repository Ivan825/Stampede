import { ChevronDown, ChevronRight, Plus, X } from 'lucide-react';
import { useState } from 'react';
import type { RunOverrides } from '@/api/types';
import { shapes } from '@/api/types';
import { Button, Field, Input, Select } from '@/components/ui';
import type { EnvRow, OverridesForm, overrideErrors } from './runForm';

// Form pieces shared by the new run and schedule dialogs.

export function OverridesFields({
  ov,
  setOv,
  errors,
  id,
  defaultOpen = false,
}: {
  ov: OverridesForm;
  setOv: (f: (o: OverridesForm) => OverridesForm) => void;
  errors: ReturnType<typeof overrideErrors> | Record<string, undefined>;
  id: string;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const setO = (k: keyof RunOverrides) => (e: { target: { value: string } }) =>
    setOv((o) => ({ ...o, [k]: e.target.value }));
  return (
    <div className="rounded-md border border-line">
      <button
        type="button"
        className="flex w-full items-center gap-1.5 px-3 py-2 text-left text-[13px] font-medium"
        aria-expanded={open}
        aria-controls={id}
        onClick={() => setOpen((v) => !v)}
      >
        {open ? (
          <ChevronDown className="size-4" aria-hidden />
        ) : (
          <ChevronRight className="size-4" aria-hidden />
        )}
        Load overrides
        <span className="font-normal text-muted">— optional</span>
      </button>
      {open && (
        <div id={id} className="grid grid-cols-3 gap-3 border-t border-line p-3">
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
          <Field label="Duration" error={errors.duration}>
            {(p) => (
              <Input {...p} placeholder="5m" value={ov.duration} onChange={setO('duration')} />
            )}
          </Field>
          <Field label="Virtual users" error={errors.vus}>
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
          <Field label="Rate" error={errors.rate}>
            {(p) => <Input {...p} placeholder="100/s" value={ov.rate} onChange={setO('rate')} />}
          </Field>
          <div />
          <Field label="Start level" hint="Shapes only.">
            {(p) => <Input {...p} placeholder="10/s" value={ov.start} onChange={setO('start')} />}
          </Field>
          <Field label="Max level" hint="Shapes only.">
            {(p) => <Input {...p} placeholder="500/s" value={ov.max} onChange={setO('max')} />}
          </Field>
        </div>
      )}
    </div>
  );
}

export function EnvFields({
  env,
  setEnv,
  error,
  help,
}: {
  env: EnvRow[];
  setEnv: (f: (rows: EnvRow[]) => EnvRow[]) => void;
  error?: string;
  help?: string;
}) {
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1 text-[13px] font-medium">Environment</legend>
      {env.length === 0 && (
        <p className="text-xs text-muted">
          Values for <code className="font-mono">{'${env.NAME}'}</code> in the scenario.{' '}
          {help ?? 'Use Secrets for credentials.'}
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
              setEnv((rows) => rows.map((r, j) => (j === i ? { ...r, key: e.target.value } : r)))
            }
          />
          <Input
            aria-label={`Variable ${i + 1} value`}
            placeholder="value"
            className="font-mono"
            value={row.value}
            onChange={(e) =>
              setEnv((rows) => rows.map((r, j) => (j === i ? { ...r, value: e.target.value } : r)))
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
      {error && <p className="text-xs text-fail">{error}</p>}
      <div>
        <Button size="sm" onClick={() => setEnv((rows) => [...rows, { key: '', value: '' }])}>
          <Plus className="size-3.5" aria-hidden /> Add variable
        </Button>
      </div>
    </fieldset>
  );
}
