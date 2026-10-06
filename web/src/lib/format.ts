/**
 * Number and time formatting. The latency, percentage and byte helpers mirror
 * internal/report/export.go so the UI and the exported reports read the same.
 */

/** Formats a latency given in seconds, like report.Ms in Go. */
export function ms(seconds: number | null | undefined): string {
  if (seconds == null || !Number.isFinite(seconds)) return '–';
  if (seconds >= 10) return `${seconds.toFixed(1)}s`;
  if (seconds >= 1) return `${(seconds * 1000).toFixed(0)}ms`;
  if (seconds >= 0.01) return `${(seconds * 1000).toFixed(1)}ms`;
  return `${(seconds * 1000).toFixed(2)}ms`;
}

/** Formats a fraction (0.0123) as a percentage ("1.23%"), like report.Pct. */
export function pct(fraction: number | null | undefined, digits = 2): string {
  if (fraction == null || !Number.isFinite(fraction)) return '–';
  return `${(fraction * 100).toFixed(digits)}%`;
}

/** Formats a byte count with binary units, like report.Bytes. */
export function bytes(n: number | null | undefined): string {
  if (n == null || !Number.isFinite(n)) return '–';
  const k = 1024;
  if (n >= k ** 3) return `${(n / k ** 3).toFixed(2)} GiB`;
  if (n >= k ** 2) return `${(n / k ** 2).toFixed(2)} MiB`;
  if (n >= k) return `${(n / k).toFixed(1)} KiB`;
  return `${Math.round(n)} B`;
}

/** Whole numbers stay whole, others get one decimal, like report.num. */
export function num(f: number | null | undefined): string {
  if (f == null || !Number.isFinite(f)) return '–';
  return Number.isInteger(f) ? String(f) : f.toFixed(1);
}

/** Groups thousands for counts: 1234567 -> "1,234,567". */
export function count(n: number | null | undefined): string {
  if (n == null || !Number.isFinite(n)) return '–';
  return Math.round(n).toLocaleString('en-US');
}

/** Requests per second with one decimal below 100 and none above. */
export function rate(rps: number | null | undefined): string {
  if (rps == null || !Number.isFinite(rps)) return '–';
  if (rps >= 100) return Math.round(rps).toLocaleString('en-US');
  return rps.toFixed(1);
}

/** Compact duration for axis ticks and summaries, like fmtSecs in Go. */
export function secs(s: number): string {
  if (s >= 3600) return `${(s / 3600).toFixed(1)}h`;
  if (s >= 120) return `${(s / 60).toFixed(0)}m`;
  return `${s.toFixed(0)}s`;
}

/** Clock-style duration: 75 -> "1:15", 3725 -> "1:02:05". */
export function clock(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const pad = (v: number) => String(v).padStart(2, '0');
  return h > 0 ? `${h}:${pad(m)}:${pad(sec)}` : `${m}:${pad(sec)}`;
}

/** Human duration: 90 -> "1m 30s", 7200 -> "2h". */
export function humanDuration(totalSeconds: number | null | undefined): string {
  if (totalSeconds == null || !Number.isFinite(totalSeconds)) return '–';
  const s = Math.round(totalSeconds);
  if (s < 60) return `${s}s`;
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const parts: string[] = [];
  if (h) parts.push(`${h}h`);
  if (m) parts.push(`${m}m`);
  if (sec && !h) parts.push(`${sec}s`);
  return parts.join(' ');
}

const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });

/** "3 minutes ago", relative to now (or a given reference). */
export function relativeTime(iso: string | null | undefined, now: number = Date.now()): string {
  if (!iso) return '–';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '–';
  const diff = (t - now) / 1000;
  const abs = Math.abs(diff);
  if (abs < 45) return 'just now';
  if (abs < 3600) return rtf.format(Math.round(diff / 60), 'minute');
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), 'hour');
  if (abs < 86400 * 30) return rtf.format(Math.round(diff / 86400), 'day');
  return dateTime(iso);
}

/** "6 Oct 2026, 14:03". */
export function dateTime(iso: string | null | undefined): string {
  if (!iso) return '–';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '–';
  return d.toLocaleString('en-GB', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

/** Axis label for a latency in seconds: "250ms", "1.2s". */
export function axisMs(seconds: number): string {
  if (seconds >= 1) return `${seconds.toFixed(1)}s`;
  if (seconds >= 0.01 || seconds === 0) return `${Math.round(seconds * 1000)}ms`;
  return `${(seconds * 1000).toFixed(1)}ms`;
}

/** Axis label for a fraction: more decimals when values are small. */
export function axisPct(fraction: number): string {
  if (fraction === 0) return '0%';
  if (fraction < 0.001) return `${(fraction * 100).toFixed(3)}%`;
  if (fraction < 0.01) return `${(fraction * 100).toFixed(2)}%`;
  return `${(fraction * 100).toFixed(1)}%`;
}

/** The unit for a load level in a given mode, like report.Unit. */
export function unit(mode: string | undefined): string {
  return mode === 'rate' ? '/s' : ' VUs';
}

/** Formats a load level with its unit: 50 rate -> "50/s", 20 vus -> "20 VUs". */
export function load(level: number | null | undefined, mode: string | undefined): string {
  if (level == null) return '–';
  return `${num(level)}${unit(mode)}`;
}
