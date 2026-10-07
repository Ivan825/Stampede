import { QueryClientProvider } from '@tanstack/react-query';
import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useRunWorkers } from '@/api/queries';
import type { RunWorkers } from '@/api/types';
import { isActive } from '@/api/types';
import { testQueryClient } from '@/test/render';
import { installMockApi } from '@/test/server';
import { WorkerHealthGrid } from './WorkerHealth';

const now = Date.parse('2026-10-07T10:00:30Z');

const data: RunWorkers = {
  live: true,
  workers: [
    {
      id: 'w1',
      name: 'worker-eu-1',
      region: 'eu-west-1',
      status: 'running',
      saturated: false,
      cpuPercent: 41.6,
      schedLagP99: 0.0008,
      lastHeartbeatAt: '2026-10-07T10:00:29Z',
    },
    {
      id: 'w2',
      name: 'worker-eu-2',
      region: 'eu-west-1',
      status: 'saturated',
      saturated: true,
      reasons: ['cpu 92% above 85% for 10s'],
      cpuPercent: 92,
      schedLagP99: 0.0141,
      lastHeartbeatAt: '2026-10-07T10:00:30Z',
    },
    {
      id: 'w3',
      name: 'worker-us-1',
      status: 'lost',
      saturated: false,
      cpuPercent: 0,
      schedLagP99: 0,
      lastHeartbeatAt: '2026-10-07T09:58:00Z',
    },
  ],
};

describe('WorkerHealthGrid', () => {
  it('shows each worker with CPU, scheduling lag, saturation and heartbeat', () => {
    render(<WorkerHealthGrid data={data} now={now} />);
    expect(screen.getByText('3 workers · 1 saturated · 1 lost')).toBeInTheDocument();
    const grid = within(screen.getByRole('list', { name: 'Worker health' }));
    const one = within(grid.getByRole('listitem', { name: 'worker-eu-1' }));
    expect(one.getByText('running')).toBeInTheDocument();
    expect(one.getByText('eu-west-1')).toBeInTheDocument();
    expect(one.getByText('42%')).toBeInTheDocument();
    expect(one.getByRole('meter', { name: 'worker-eu-1 CPU' })).toHaveAttribute(
      'aria-valuenow',
      '42',
    );
    expect(one.getByText('0.80ms')).toBeInTheDocument();
    expect(one.getByText('1s ago')).toBeInTheDocument();
    expect(one.queryByRole('note')).not.toBeInTheDocument();

    const two = within(grid.getByRole('listitem', { name: 'worker-eu-2' }));
    expect(two.getByText('saturated')).toBeInTheDocument();
    expect(two.getByText('14.1ms')).toHaveClass('text-warn');
    expect(two.getByRole('note')).toHaveTextContent('Saturated: cpu 92% above 85% for 10s.');

    const three = within(grid.getByRole('listitem', { name: 'worker-us-1' }));
    expect(three.getByText('lost')).toBeInTheDocument();
    expect(three.getByText('–')).toBeInTheDocument();
    expect(three.getByText('2 minutes ago')).toBeInTheDocument();
    expect(three.getByRole('note')).toHaveTextContent('No heartbeat');
  });

  it('explains an empty grid', () => {
    const { rerender } = render(<WorkerHealthGrid data={{ live: true, workers: [] }} now={now} />);
    expect(screen.getByText('Waiting for the first health sample.')).toBeInTheDocument();
    rerender(<WorkerHealthGrid data={{ live: false, workers: [] }} now={now} />);
    expect(
      screen.getByText('Health is shown while the run executes on this server.'),
    ).toBeInTheDocument();
    expect(screen.queryByRole('list')).not.toBeInTheDocument();
  });
});

function Live({ runId, active }: { runId: string; active: boolean }) {
  const q = useRunWorkers(runId, active);
  return <WorkerHealthGrid data={q.data} error={q.error} now={Date.now()} />;
}

describe('useRunWorkers', () => {
  it("polls the run's workers from the API while it runs", async () => {
    const db = installMockApi();
    const run = db.runs.find((r) => isActive(r.status))!;
    render(
      <QueryClientProvider client={testQueryClient()}>
        <Live runId={run.id} active />
      </QueryClientProvider>,
    );
    const grid = await screen.findByRole('list', { name: 'Worker health' });
    expect(within(grid).getByRole('listitem', { name: 'worker-eu-1' })).toBeInTheDocument();
    // worker-eu-2 saturates 25s into the run; the fixture run is 40s in.
    expect(
      within(within(grid).getByRole('listitem', { name: 'worker-eu-2' })).getByText('saturated'),
    ).toBeInTheDocument();
  });

  it('does not ask for a finished run', () => {
    const db = installMockApi();
    const run = db.runs.find((r) => !isActive(r.status))!;
    render(
      <QueryClientProvider client={testQueryClient()}>
        <Live runId={run.id} active={false} />
      </QueryClientProvider>,
    );
    expect(screen.getByText('Loading…')).toBeInTheDocument();
  });
});
