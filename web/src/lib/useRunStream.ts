import { useEffect, useReducer, useRef } from 'react';
import { liveUrl } from '@/api/client';
import type { Point, Run, RunEvent } from '@/api/types';
import { isTerminal } from '@/api/types';

export type StreamState = 'connecting' | 'open' | 'reconnecting' | 'closed';

export interface RunStream {
  points: Point[];
  events: RunEvent[];
  /** The latest Run from a `status` event, if any arrived. */
  run: Run | null;
  state: StreamState;
}

type Action =
  | { type: 'seed'; points: Point[] }
  | { type: 'point'; point: Point }
  | { type: 'status'; run: Run }
  | { type: 'event'; event: RunEvent }
  | { type: 'state'; state: StreamState }
  | { type: 'reset' };

const MAX_EVENTS = 500;

const initial: RunStream = { points: [], events: [], run: null, state: 'connecting' };

/** Inserts or replaces points by time, keeping them sorted. */
export function mergePoints(existing: Point[], incoming: Point[]): Point[] {
  if (incoming.length === 0) return existing;
  const last = existing[existing.length - 1];
  // Fast path: a single new point after the last one.
  if (incoming.length === 1 && (!last || incoming[0]!.t > last.t)) {
    return [...existing, incoming[0]!];
  }
  const byT = new Map<number, Point>();
  for (const p of existing) byT.set(p.t, p);
  for (const p of incoming) byT.set(p.t, p);
  return [...byT.values()].sort((a, b) => a.t - b.t);
}

function reducer(s: RunStream, a: Action): RunStream {
  switch (a.type) {
    case 'seed':
      return { ...s, points: mergePoints(s.points, a.points) };
    case 'point':
      return { ...s, points: mergePoints(s.points, [a.point]) };
    case 'status':
      return { ...s, run: a.run };
    case 'event': {
      const events = [a.event, ...s.events];
      return { ...s, events: events.length > MAX_EVENTS ? events.slice(0, MAX_EVENTS) : events };
    }
    case 'state':
      return s.state === a.state ? s : { ...s, state: a.state };
    case 'reset':
      return initial;
  }
}

export interface StreamOptions {
  enabled?: boolean;
  /** Points already recorded (from GET /runs/{id}/timeline). */
  seed?: Point[];
  /** Called once when a `status` event reports a terminal state. */
  onFinished?: (run: Run) => void;
  /** Base and max reconnect delays in ms. */
  retryMs?: number;
  maxRetryMs?: number;
}

function parse(data: unknown): unknown {
  if (typeof data !== 'string') return null;
  try {
    return JSON.parse(data);
  } catch {
    return null;
  }
}

/**
 * Subscribes to GET /runs/{id}/live. Collects `point`, `status` and `event`
 * messages, reconnects with exponential backoff when the stream drops, and
 * stops once the run reaches a terminal state.
 */
export function useRunStream(runId: string, opts: StreamOptions = {}): RunStream {
  const { enabled = true, seed, retryMs = 1000, maxRetryMs = 15_000 } = opts;
  const [state, dispatch] = useReducer(reducer, initial);
  const onFinished = useRef(opts.onFinished);
  useEffect(() => {
    onFinished.current = opts.onFinished;
  });

  // Reset when switching runs; declared before the seed and stream effects
  // so it runs first.
  useEffect(() => {
    dispatch({ type: 'reset' });
  }, [runId]);

  useEffect(() => {
    if (seed && seed.length) dispatch({ type: 'seed', points: seed });
  }, [seed]);

  useEffect(() => {
    if (!enabled) return;
    let source: EventSource | null = null;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let attempt = 0;
    let done = false;

    const connect = () => {
      if (done) return;
      dispatch({ type: 'state', state: attempt === 0 ? 'connecting' : 'reconnecting' });
      const es = new EventSource(liveUrl(runId), { withCredentials: true });
      source = es;

      es.onopen = () => {
        attempt = 0;
        dispatch({ type: 'state', state: 'open' });
      };
      es.addEventListener('point', (e) => {
        const p = parse(e.data) as Point | null;
        if (p) dispatch({ type: 'point', point: p });
      });
      es.addEventListener('event', (e) => {
        const ev = parse(e.data) as RunEvent | null;
        if (ev) dispatch({ type: 'event', event: ev });
      });
      es.addEventListener('status', (e) => {
        const run = parse(e.data) as Run | null;
        if (!run) return;
        dispatch({ type: 'status', run });
        if (isTerminal(run.status) && !done) {
          done = true;
          es.close();
          dispatch({ type: 'state', state: 'closed' });
          onFinished.current?.(run);
        }
      });
      es.onerror = () => {
        if (done) return;
        // The browser retries on its own while CONNECTING; take over once
        // it gives up (CLOSED), e.g. after a non-200 response or proxy cut.
        if (es.readyState === EventSource.CLOSED) {
          es.close();
          const delay = Math.min(maxRetryMs, retryMs * 2 ** attempt);
          attempt++;
          dispatch({ type: 'state', state: 'reconnecting' });
          timer = setTimeout(connect, delay);
        } else {
          dispatch({ type: 'state', state: 'reconnecting' });
        }
      };
    };

    connect();
    return () => {
      done = true;
      clearTimeout(timer);
      source?.close();
    };
  }, [runId, enabled, retryMs, maxRetryMs]);

  return state;
}
