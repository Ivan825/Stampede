import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { RunEvent } from '@/api/types';
import { renderApp } from '@/test/render';
import { installMockApi } from '@/test/server';
import { dryRunGate, eventTone, mergeEvents } from './eventLog';

// ECharts needs a canvas, which jsdom lacks.
vi.mock('./ReportCharts', async (orig) => {
  const real = await orig<typeof import('./ReportCharts')>();
  return {
    ...real,
    CurveChart: () => <div />,
    ReportCharts: () => <div />,
    TargetMetricCharts: () => <div />,
  };
});

const ev = (at: number, type: string, message: string, details?: RunEvent['details']) => ({
  at: new Date(Date.UTC(2026, 9, 7, 8, 0, at)).toISOString(),
  type,
  message,
  ...(details ? { details } : {}),
});

describe('run events', () => {
  it('merges recorded and streamed events, newest first, without duplicates', () => {
    const a = ev(1, 'worker.assigned', '2 workers');
    const b = ev(2, 'dryrun.started', 'dry run');
    const c = ev(3, 'worker.saturated', 'cpu');
    expect(mergeEvents([a, b], [c, b])).toEqual([c, b, a]);
    expect(mergeEvents([], [])).toEqual([]);
  });

  it('reads the dry-run gate from its events', () => {
    expect(dryRunGate([ev(1, 'worker.assigned', 'x')])).toBeNull();
    const running = dryRunGate([
      ev(1, 'dryrun.started', 'started'),
      ev(2, 'dryrun.journey', 'browse passed', { journey: 'browse', ok: true }),
    ]);
    expect(running).toEqual({ state: 'running', journeys: [{ journey: 'browse', ok: true }] });
    const failed = dryRunGate([
      ev(4, 'dryrun.failed', '1 of 2 journeys failed the dry run; no load was started'),
      ev(3, 'dryrun.journey', 'login failed', { journey: 'login', ok: false, problem: '404' }),
      ev(2, 'dryrun.journey', 'browse passed', { journey: 'browse', ok: true }),
      ev(1, 'dryrun.started', 'started'),
    ]);
    expect(failed).toEqual({
      state: 'failed',
      summary: '1 of 2 journeys failed the dry run; no load was started',
      journeys: [
        { journey: 'browse', ok: true },
        { journey: 'login', ok: false, problem: '404' },
      ],
    });
    // A dry run that could not finish has no journey results.
    expect(
      dryRunGate([ev(1, 'dryrun.started', 's'), ev(2, 'dryrun.failed', 'could not finish')]),
    ).toEqual({ state: 'failed', summary: 'could not finish', journeys: [] });
  });

  it('marks failures, warnings and passes', () => {
    expect(eventTone(ev(1, 'dryrun.journey', 'x', { journey: 'a', ok: false }))).toBe('fail');
    expect(eventTone(ev(1, 'dryrun.journey', 'x', { journey: 'a', ok: true }))).toBe('pass');
    expect(eventTone(ev(1, 'dryrun.failed', 'x'))).toBe('fail');
    expect(eventTone(ev(1, 'worker.lost', 'x'))).toBe('fail');
    expect(eventTone(ev(1, 'worker.saturated', 'x'))).toBe('warn');
    expect(eventTone(ev(1, 'worker.assigned', 'x'))).toBe('info');
  });
});

describe('Run page events', () => {
  it('explains a run the dry-run gate refused', async () => {
    const db = installMockApi();
    const run = db.runs.find((r) => r.error?.includes('required dry run'))!;
    renderApp(`/runs/${run.id}`);
    const gate = within(await screen.findByRole('region', { name: 'Dry run before load' }));
    // The dry run's result, and the failed journey's.
    await waitFor(() => expect(gate.getAllByText('FAILED')).toHaveLength(2));
    expect(
      gate.getByText('1 of 2 journeys failed the dry run; no load was started.'),
    ).toBeInTheDocument();
    const rows = within(gate.getByRole('table', { name: 'Dry-run journeys' }));
    const login = rows.getByText('login').closest('tr')!;
    expect(within(login).getByText('FAILED')).toBeInTheDocument();
    expect(
      within(login).getByText('step 1 (POST /api/login): status 200 expected, got 404'),
    ).toBeInTheDocument();
    const browse = rows.getByText('browse').closest('tr')!;
    expect(within(browse).getByText('PASSED')).toBeInTheDocument();

    expect(screen.getByRole('alert')).toHaveTextContent('the required dry run failed');
    expect(await screen.findByText(/No report for this run/)).toBeInTheDocument();

    const list = within(screen.getByRole('list', { name: 'Run events' }));
    const items = list.getAllByRole('listitem');
    // Newest first.
    expect(items[0]).toHaveTextContent('dryrun.failed');
    expect(items[items.length - 1]).toHaveTextContent('dryrun.started');
    expect(list.getByText(/journey login failed its dry run/)).toBeInTheDocument();
  });

  it('lists a finished run’s events under its report', async () => {
    const db = installMockApi();
    const run = db.runs.find((r) => r.status === 'completed')!;
    renderApp(`/runs/${run.id}`);
    const events = within(await screen.findByRole('region', { name: 'Events' }));
    expect(await events.findByText('2 workers assigned in eu-west-1')).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Dry run before load' })).not.toBeInTheDocument();
  });
});
