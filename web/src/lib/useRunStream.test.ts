import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Point, Run } from '@/api/types';
import { mergePoints, useRunStream } from './useRunStream';

/** A controllable stand-in for the browser's EventSource. */
class FakeEventSource {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 2;
  static instances: FakeEventSource[] = [];

  readonly url: string;
  readyState = FakeEventSource.CONNECTING;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  private listeners = new Map<string, ((e: MessageEvent) => void)[]>();

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, fn: (e: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  close() {
    this.closed = true;
    this.readyState = FakeEventSource.CLOSED;
  }
  // Test controls.
  open() {
    this.readyState = FakeEventSource.OPEN;
    this.onopen?.();
  }
  emit(type: string, data: unknown) {
    const e = new MessageEvent(type, { data: JSON.stringify(data) });
    this.listeners.get(type)?.forEach((fn) => fn(e));
  }
  fail(permanently: boolean) {
    this.readyState = permanently ? FakeEventSource.CLOSED : FakeEventSource.CONNECTING;
    this.onerror?.();
  }
}

const point = (t: number, rps = 10): Point => ({
  t,
  rps,
  errorRate: 0,
  p50: 0.01,
  p95: 0.02,
  p99: 0.03,
  vus: 5,
  planned: 10,
  dropped: 0,
});

const run = (status: Run['status']): Run => ({
  id: 'run-1',
  projectId: 'p',
  scenarioId: 's',
  scenarioVersion: 1,
  targetId: 't',
  status,
  createdAt: '2026-10-06T00:00:00Z',
});

const last = () => FakeEventSource.instances[FakeEventSource.instances.length - 1]!;

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal('EventSource', FakeEventSource);
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('mergePoints', () => {
  it('appends in order and replaces points with the same t', () => {
    const a = mergePoints([point(0), point(1)], [point(2)]);
    expect(a.map((p) => p.t)).toEqual([0, 1, 2]);
    const b = mergePoints(a, [point(1, 99), point(3)]);
    expect(b.map((p) => p.t)).toEqual([0, 1, 2, 3]);
    expect(b[1]!.rps).toBe(99);
  });
});

describe('useRunStream', () => {
  it('connects to the live endpoint and collects points, events and status', () => {
    const { result } = renderHook(() => useRunStream('run-1'));
    const es = last();
    expect(es.url).toBe('/api/v1/runs/run-1/live');
    expect(result.current.state).toBe('connecting');

    act(() => {
      es.open();
      es.emit('point', point(0));
      es.emit('point', point(1));
      es.emit('event', { type: 'worker.lost', message: 'w1 lost', at: 'x' });
      es.emit('status', run('running'));
    });
    expect(result.current.state).toBe('open');
    expect(result.current.points.map((p) => p.t)).toEqual([0, 1]);
    expect(result.current.events[0]!.type).toBe('worker.lost');
    expect(result.current.run?.status).toBe('running');
  });

  it('merges seeded timeline points with streamed ones', () => {
    const seed = [point(0), point(1)];
    const { result } = renderHook(() => useRunStream('run-1', { seed }));
    act(() => {
      last().emit('point', point(1, 50));
      last().emit('point', point(2));
    });
    expect(result.current.points.map((p) => [p.t, p.rps])).toEqual([
      [0, 10],
      [1, 50],
      [2, 10],
    ]);
  });

  it('ignores malformed messages', () => {
    const { result } = renderHook(() => useRunStream('run-1'));
    act(() => {
      const e = new MessageEvent('point', { data: '{not json' });
      // Deliver a raw malformed event.
      (last() as unknown as { listeners: Map<string, ((e: MessageEvent) => void)[]> }).listeners
        .get('point')
        ?.forEach((fn) => fn(e));
    });
    expect(result.current.points).toHaveLength(0);
  });

  it('reconnects with backoff after the stream closes', () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useRunStream('run-1', { retryMs: 1000 }));
    expect(FakeEventSource.instances).toHaveLength(1);

    // A transient error: the browser retries by itself.
    act(() => {
      last().fail(false);
    });
    expect(result.current.state).toBe('reconnecting');
    expect(FakeEventSource.instances).toHaveLength(1);

    // The browser gave up: the hook reconnects after the backoff.
    act(() => {
      last().fail(true);
    });
    expect(FakeEventSource.instances[0]!.closed).toBe(true);
    act(() => {
      vi.advanceTimersByTime(999);
    });
    expect(FakeEventSource.instances).toHaveLength(1);
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(FakeEventSource.instances).toHaveLength(2);

    // Second failure doubles the delay.
    act(() => {
      last().fail(true);
    });
    act(() => {
      vi.advanceTimersByTime(1999);
    });
    expect(FakeEventSource.instances).toHaveLength(2);
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(FakeEventSource.instances).toHaveLength(3);

    // A successful open resets the backoff.
    act(() => {
      last().open();
    });
    expect(result.current.state).toBe('open');
  });

  it('closes and reports once the run finishes', () => {
    vi.useFakeTimers();
    const onFinished = vi.fn<(r: Run) => void>();
    const { result } = renderHook(() => useRunStream('run-1', { onFinished }));
    const es = last();
    act(() => {
      es.open();
      es.emit('status', run('analyzing'));
    });
    expect(onFinished).not.toHaveBeenCalled();
    act(() => {
      es.emit('status', run('completed'));
    });
    expect(onFinished).toHaveBeenCalledTimes(1);
    expect(onFinished.mock.calls[0]![0].status).toBe('completed');
    expect(es.closed).toBe(true);
    expect(result.current.state).toBe('closed');

    // No reconnect after completion.
    act(() => {
      es.fail(true);
    });
    act(() => {
      vi.advanceTimersByTime(60_000);
    });
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it('does not connect when disabled and closes on unmount', () => {
    renderHook(() => useRunStream('run-1', { enabled: false }));
    expect(FakeEventSource.instances).toHaveLength(0);
    const { unmount } = renderHook(() => useRunStream('run-2'));
    const es = last();
    unmount();
    expect(es.closed).toBe(true);
  });
});
