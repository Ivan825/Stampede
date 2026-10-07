import type { Caps, LimitCaps } from '@/api/types';
import { Field, Input } from '@/components/ui';
import { humanDuration, num } from '@/lib/format';

/** Caps as typed: empty means no cap. Duration is in seconds. */
export interface CapsForm {
  maxRate: string;
  maxVUs: string;
  maxDur: string;
}

export function capsForm(c: Caps | undefined): CapsForm {
  return {
    maxRate: c?.maxRate?.toString() ?? '',
    maxVUs: c?.maxVUs?.toString() ?? '',
    maxDur: c?.maxDurationSeconds?.toString() ?? '',
  };
}

const isNum = (v: string) => v.trim() === '' || (Number.isFinite(Number(v)) && Number(v) > 0);
const isInt = (v: string) => v.trim() === '' || (Number.isInteger(Number(v)) && Number(v) > 0);

export function capsErrors(f: CapsForm) {
  return {
    maxRate: isNum(f.maxRate) ? undefined : 'A positive number.',
    maxVUs: isInt(f.maxVUs) ? undefined : 'A positive whole number.',
    maxDur: isInt(f.maxDur) ? undefined : 'Seconds, a positive whole number.',
  };
}

export function toCaps(f: CapsForm): Caps {
  const c: Caps = {};
  if (f.maxRate.trim()) c.maxRate = Number(f.maxRate);
  if (f.maxVUs.trim()) c.maxVUs = Number(f.maxVUs);
  if (f.maxDur.trim()) c.maxDurationSeconds = Number(f.maxDur);
  return c;
}

/** "≤ 100/s · ≤ 500 VUs · ≤ 1h", or "No caps". */
export function capsText(c: LimitCaps | undefined): string {
  const parts = [
    c?.maxRate != null && `≤ ${num(c.maxRate)}/s`,
    c?.maxVUs != null && `≤ ${c.maxVUs.toLocaleString('en-US')} VUs`,
    c?.maxDurationSeconds != null && `≤ ${humanDuration(c.maxDurationSeconds)}`,
  ].filter(Boolean);
  return parts.length ? parts.join(' · ') : 'No caps';
}

/** Max rate, VUs and duration inputs; leave one empty for no cap. */
export function CapsFields({
  form,
  setForm,
  errors,
  legend = 'Caps',
  hint = 'Leave a field empty for no cap.',
}: {
  form: CapsForm;
  setForm: (f: CapsForm) => void;
  errors: Partial<Record<keyof CapsForm, string>>;
  legend?: string;
  hint?: string;
}) {
  return (
    <fieldset>
      <legend className="text-[13px] font-medium">{legend}</legend>
      {hint && <p className="mb-2 text-xs text-muted">{hint}</p>}
      <div className="grid gap-3 sm:grid-cols-3">
        <Field label="Max rate (/s)" hint="Iterations per second." error={errors.maxRate}>
          {(p) => (
            <Input
              {...p}
              inputMode="decimal"
              value={form.maxRate}
              onChange={(e) => setForm({ ...form, maxRate: e.target.value })}
            />
          )}
        </Field>
        <Field label="Max VUs" error={errors.maxVUs}>
          {(p) => (
            <Input
              {...p}
              inputMode="numeric"
              value={form.maxVUs}
              onChange={(e) => setForm({ ...form, maxVUs: e.target.value })}
            />
          )}
        </Field>
        <Field label="Max duration (s)" error={errors.maxDur}>
          {(p) => (
            <Input
              {...p}
              inputMode="numeric"
              value={form.maxDur}
              onChange={(e) => setForm({ ...form, maxDur: e.target.value })}
            />
          )}
        </Field>
      </div>
    </fieldset>
  );
}
