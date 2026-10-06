import type { RunOverrides } from '@/api/types';

// Form state shared by the new run and schedule dialogs, which take the
// same overrides and environment.

const ratePattern = /^[0-9.]+(\/(s|sec|second|m|min|minute|h|hour))?$/;
const durationPattern = /^([0-9.]+(ns|us|µs|ms|s|m|h|d)?)+$/;

export type OverridesForm = Record<keyof RunOverrides, string>;

export function overridesForm(o: RunOverrides = {}): OverridesForm {
  return {
    shape: o.shape ?? '',
    mode: o.mode ?? '',
    vus: o.vus != null ? String(o.vus) : '',
    rate: o.rate ?? '',
    duration: o.duration ?? '',
    start: o.start ?? '',
    max: o.max ?? '',
  };
}

export function overrideErrors(ov: OverridesForm) {
  return {
    vus:
      ov.vus && !(Number.isInteger(Number(ov.vus)) && Number(ov.vus) >= 1)
        ? 'A whole number, at least 1.'
        : undefined,
    rate: ov.rate && !ratePattern.test(ov.rate.trim()) ? 'For example 50/s or 3000/m.' : undefined,
    duration:
      ov.duration && !durationPattern.test(ov.duration.trim())
        ? 'For example 30s, 5m or 1h30m.'
        : undefined,
  };
}

export function toOverrides(ov: OverridesForm): RunOverrides {
  const overrides: RunOverrides = {};
  if (ov.shape) overrides.shape = ov.shape;
  if (ov.mode === 'vus' || ov.mode === 'rate') overrides.mode = ov.mode;
  if (ov.vus) overrides.vus = Number(ov.vus);
  if (ov.rate.trim()) overrides.rate = ov.rate.trim();
  if (ov.duration.trim()) overrides.duration = ov.duration.trim();
  if (ov.start.trim()) overrides.start = ov.start.trim();
  if (ov.max.trim()) overrides.max = ov.max.trim();
  return overrides;
}

export interface EnvRow {
  key: string;
  value: string;
}

export function envRows(env: Record<string, string> = {}): EnvRow[] {
  return Object.entries(env).map(([key, value]) => ({ key, value }));
}

export function envError(env: EnvRow[]): string | undefined {
  return env.some((r) => r.key && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(r.key))
    ? 'Variable names use letters, digits and underscores.'
    : undefined;
}

export function toEnv(env: EnvRow[]): Record<string, string> {
  return Object.fromEntries(env.filter((r) => r.key).map((r) => [r.key, r.value]));
}
