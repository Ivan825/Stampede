import { Field, Input } from '@/components/ui';
import type { CapsForm } from './capsForm';

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
