import type { RunEvent } from '@/api/types';

const keyOf = (e: RunEvent) => `${e.at}|${e.type}|${e.message}`;

/**
 * Events from GET /runs/{id}/events (oldest first) and the live stream
 * (newest first), merged without duplicates, newest first.
 */
export function mergeEvents(recorded: RunEvent[], streamed: RunEvent[]): RunEvent[] {
  const seen = new Set<string>();
  const out: RunEvent[] = [];
  for (const e of [...streamed, ...[...recorded].reverse()]) {
    const k = keyOf(e);
    if (seen.has(k)) continue;
    seen.add(k);
    out.push(e);
  }
  return out.sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
}

export interface GateJourney {
  journey: string;
  ok: boolean;
  problem?: string;
}

export interface DryRunGate {
  state: 'running' | 'passed' | 'failed';
  /** The passed or failed event's message, once the dry run ended. */
  summary?: string;
  journeys: GateJourney[];
}

/**
 * The dry run a project can require before load, from a run's events
 * (any order), or null when the run had none.
 */
export function dryRunGate(events: RunEvent[]): DryRunGate | null {
  const evs = events
    .filter((e) => e.type.startsWith('dryrun.'))
    .sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
  if (evs.length === 0) return null;
  const journeys: GateJourney[] = [];
  let gate: DryRunGate = { state: 'running', journeys };
  for (const e of evs) {
    const d = e.details ?? {};
    if (e.type === 'dryrun.journey' && typeof d.journey === 'string') {
      journeys.push({
        journey: d.journey,
        ok: d.ok === true,
        ...(typeof d.problem === 'string' ? { problem: d.problem } : {}),
      });
    } else if (e.type === 'dryrun.passed') {
      gate = { state: 'passed', summary: e.message, journeys };
    } else if (e.type === 'dryrun.failed') {
      gate = { state: 'failed', summary: e.message, journeys };
    }
  }
  return gate;
}

/** Whether an event reports a failure, for its icon. */
export function eventTone(e: RunEvent): 'fail' | 'warn' | 'pass' | 'info' {
  if (e.type === 'dryrun.failed' || (e.type === 'dryrun.journey' && e.details?.ok === false))
    return 'fail';
  if (e.type === 'dryrun.passed' || (e.type === 'dryrun.journey' && e.details?.ok === true))
    return 'pass';
  if (e.type.includes('lost')) return 'fail';
  if (e.type.includes('saturat') || e.type.includes('safety') || e.type.includes('cap'))
    return 'warn';
  return 'info';
}
