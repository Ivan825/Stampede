import type { RunOverrides } from '@/api/types';

// Form state shared by the new run and schedule dialogs, which take the
// same overrides and environment.

const ratePattern = /^[0-9.]+(\/(s|sec|second|m|min|minute|h|hour))?$/;
const durationPattern = /^([0-9.]+(ns|us|µs|ms|s|m|h|d)?)+$/;

// Region splits have their own rows (see RegionRow).
export type OverridesForm = Record<Exclude<keyof RunOverrides, 'regions'>, string>;

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

/** One region of a run's load split: a worker region and its share. */
export interface RegionRow {
  region: string;
  percent: string;
}

export function regionRows(regions: Record<string, number> = {}): RegionRow[] {
  return Object.entries(regions).map(([region, percent]) => ({ region, percent: String(percent) }));
}

/** Checks a region split as the server does: names, shares above 0, 100% in all. */
export function regionsError(rows: RegionRow[]): string | undefined {
  const used = rows.filter((r) => r.region.trim() || r.percent.trim());
  if (used.length === 0) return undefined;
  if (used.some((r) => !/^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$/.test(r.region.trim())))
    return 'Region names use letters, digits, ".", "_" or "-".';
  const names = used.map((r) => r.region.trim());
  if (new Set(names).size !== names.length) return 'Each region once.';
  const shares = used.map((r) => Number(r.percent.trim().replace(/%$/, '')));
  if (shares.some((p) => !(p > 0 && p <= 100))) return 'Shares are percentages above 0.';
  const total = shares.reduce((a, b) => a + b, 0);
  if (Math.abs(total - 100) > 0.1) return `The shares add up to ${total}%, not 100%.`;
  return undefined;
}

export function toRegions(rows: RegionRow[]): Record<string, number> | undefined {
  const used = rows.filter((r) => r.region.trim());
  if (used.length === 0) return undefined;
  return Object.fromEntries(
    used.map((r) => [r.region.trim(), Number(r.percent.trim().replace(/%$/, ''))]),
  );
}
