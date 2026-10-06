// A small cron evaluator for the mock API, so the demo can preview and show
// next run times. The server's parser (internal/cron) is the real one; this
// covers the same syntax but not its daylight-saving rules.

const names: Record<string, number> = {
  jan: 1, feb: 2, mar: 3, apr: 4, may: 5, jun: 6, jul: 7, aug: 8, sep: 9, oct: 10, nov: 11, dec: 12,
  sun: 0, mon: 1, tue: 2, wed: 3, thu: 4, fri: 5, sat: 6,
}; // prettier-ignore

const macros: Record<string, string> = {
  '@yearly': '0 0 1 1 *',
  '@annually': '0 0 1 1 *',
  '@monthly': '0 0 1 * *',
  '@weekly': '0 0 * * 0',
  '@daily': '0 0 * * *',
  '@midnight': '0 0 * * *',
  '@hourly': '0 * * * *',
};

const fields: [string, number, number][] = [
  ['minute', 0, 59],
  ['hour', 0, 23],
  ['day of month', 1, 31],
  ['month', 1, 12],
  ['day of week', 0, 7],
];

function value(s: string, field: string, min: number, max: number): number {
  const n = names[s.toLowerCase()] ?? (/^\d+$/.test(s) ? Number(s) : NaN);
  if (Number.isNaN(n)) throw new Error(`${field}: "${s}" is not a number`);
  if (n < min || n > max) throw new Error(`${field}: ${n} is out of range ${min}-${max}`);
  return n;
}

function parseField(text: string, [field, min, max]: [string, number, number]): Set<number> {
  const out = new Set<number>();
  for (const part of text.split(',')) {
    const [range = '', stepText] = part.split('/');
    const step = stepText === undefined ? 1 : Number(stepText);
    if (!Number.isInteger(step) || step < 1)
      throw new Error(`${field}: step "${stepText ?? ''}" must be a positive number`);
    let lo = min;
    let hi = field === 'day of week' ? 6 : max;
    if (range !== '*') {
      const [a = '', b] = range.split('-');
      lo = value(a, field, min, max);
      hi = b !== undefined ? value(b, field, min, max) : stepText !== undefined ? max : lo;
    }
    for (let v = lo; v <= hi; v += step) out.add(field === 'day of week' && v === 7 ? 0 : v);
  }
  return out;
}

export interface Cron {
  minute: Set<number>;
  hour: Set<number>;
  dom: Set<number>;
  month: Set<number>;
  dow: Set<number>;
  domStar: boolean;
  dowStar: boolean;
}

/** Parses a five-field expression or macro; throws with a readable message. */
export function parseCron(expr: string): Cron {
  const spec = expr.trim().startsWith('@') ? macros[expr.trim().toLowerCase()] : expr.trim();
  if (!spec) throw new Error(`unknown macro "${expr.trim()}"`);
  const parts = spec.split(/\s+/);
  if (parts.length !== 5)
    throw new Error(
      `a cron expression has 5 fields (minute hour day-of-month month day-of-week), got ${parts.length}`,
    );
  const [minute, hour, dom, month, dow] = parts.map((p, i) => parseField(p, fields[i]!));
  return {
    minute: minute!,
    hour: hour!,
    dom: dom!,
    month: month!,
    dow: dow!,
    domStar: parts[2]!.startsWith('*'),
    dowStar: parts[4]!.startsWith('*'),
  };
}

function wall(t: Date, timeZone: string) {
  const p = Object.fromEntries(
    new Intl.DateTimeFormat('en-US', {
      timeZone,
      hourCycle: 'h23',
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
      hour: 'numeric',
      minute: 'numeric',
      weekday: 'short',
    })
      .formatToParts(t)
      .map((x) => [x.type, x.value]),
  );
  return {
    month: Number(p.month),
    day: Number(p.day),
    hour: Number(p.hour),
    minute: Number(p.minute),
    weekday: names[String(p.weekday).toLowerCase()] ?? 0,
  };
}

/** The next `n` firings after `after`, as ISO strings. */
export function nextTimes(c: Cron, timeZone: string, after: Date, n: number): string[] {
  const out: string[] = [];
  let t = Math.floor(after.getTime() / 60_000) * 60_000 + 60_000;
  for (let i = 0; i < 200_000 && out.length < n; i++) {
    const w = wall(new Date(t), timeZone);
    const dom = c.dom.has(w.day);
    const dow = c.dow.has(w.weekday);
    const day = c.month.has(w.month) && (c.domStar || c.dowStar ? dom && dow : dom || dow);
    if (!day) t += (24 * 60 - w.hour * 60 - w.minute) * 60_000;
    else if (!c.hour.has(w.hour)) t += (60 - w.minute) * 60_000;
    else if (!c.minute.has(w.minute)) t += 60_000;
    else {
      out.push(new Date(t).toISOString());
      t += 60_000;
    }
  }
  return out;
}

/** Throws when `timeZone` is not an IANA zone. */
export function checkZone(timeZone: string) {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone });
  } catch {
    throw new Error(`unknown time zone "${timeZone}" (use an IANA name such as Europe/London)`);
  }
}
