import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import type { CompareRequest, Comparison, Run } from '@/api/types';
import { defaultSides } from '@/features/runs/compareSides';
import { ComparisonView } from '@/features/runs/ComparisonView';
import { renderApp, renderWithRouter } from '@/test/render';
import { installMockApi, server } from '@/test/server';

const run = (id: string, version: number, minutesAgo: number, scenarioId = 's1'): Run => ({
  id,
  projectId: 'p',
  scenarioId,
  scenarioName: scenarioId === 's1' ? 'checkout' : 'search',
  scenarioVersion: version,
  targetId: 't',
  status: 'completed',
  createdAt: new Date(Date.parse('2026-10-07T12:00:00Z') - minutesAgo * 60_000).toISOString(),
});

describe('defaultSides', () => {
  it('puts the lower of two scenario versions in A and names sides after them', () => {
    const s = defaultSides([run('r1', 4, 5), run('r2', 3, 30), run('r3', 4, 3), run('r4', 3, 40)]);
    expect(s.side).toEqual({ r1: 'b', r2: 'a', r3: 'b', r4: 'a' });
    expect([s.labelA, s.labelB]).toEqual(['v3', 'v4']);
  });

  it('splits by order otherwise: the older half is A', () => {
    const s = defaultSides([run('r1', 2, 1), run('r2', 2, 9), run('r3', 2, 5)]);
    expect(s.side).toEqual({ r2: 'a', r3: 'a', r1: 'b' });
    expect([s.labelA, s.labelB]).toEqual(['A', 'B']);
  });

  it('names sides after scenario and version for two different scenarios', () => {
    const s = defaultSides([run('r1', 2, 1, 's2'), run('r2', 7, 9)]);
    expect(s.side).toEqual({ r1: 'b', r2: 'a' });
    expect([s.labelA, s.labelB]).toEqual(['checkout v7', 'search v2']);
  });
});

const delta = (over: Partial<Comparison['metrics'][number]>): Comparison['metrics'][number] => ({
  name: 'p95',
  higherIsBetter: false,
  a: [0.1, 0.11, 0.1],
  b: [0.15, 0.16, 0.15],
  meanA: 0.1033,
  meanB: 0.1533,
  change: 0.484,
  ciLow: 0.43,
  ciHigh: 0.55,
  noiseFloor: 0.065,
  verdict: 'regression',
  ...over,
});

describe('ComparisonView', () => {
  it('shows the verdict, metrics with intervals and noise, steps and warnings', async () => {
    const c: Comparison = {
      a: { label: 'v1', runs: ['11111111-0000-4000-8000-000000000000'] },
      b: { label: 'v2', runs: ['22222222-0000-4000-8000-000000000000'] },
      comparable: false,
      problems: ['different worker counts (1 and 2)'],
      metrics: [
        delta({}),
        delta({
          name: 'error rate',
          meanA: 0,
          meanB: 0.012,
          change: null,
          ciLow: null,
          ciHigh: null,
          noiseFloor: 0.02,
          verdict: 'inconclusive',
        }),
      ],
      steps: [
        {
          journey: 'buy',
          step: 'POST /api/checkout',
          metrics: [delta({}), delta({ name: 'error rate', verdict: 'no-change', change: 0 })],
        },
      ],
      verdict: 'inconclusive',
      confidence: 0.95,
      markdown: '### ⚠️ Stampede comparison: inconclusive\n',
    };
    const user = userEvent.setup();
    renderWithRouter(<ComparisonView c={c} />);
    expect(await screen.findByText('NOT COMPARABLE')).toBeInTheDocument();
    expect(
      screen.getByText('Not comparable: different worker counts (1 and 2).'),
    ).toBeInTheDocument();
    expect(screen.getByText(/Use at least 3 runs per version/)).toBeInTheDocument();

    const tables = screen.getAllByRole('table');
    const p95 = within(tables[0]!).getByText('p95').closest('tr')!;
    expect(within(p95).getByText('103.3ms')).toBeInTheDocument();
    expect(within(p95).getByText('153.3ms')).toBeInTheDocument();
    expect(within(p95).getByText('+48.4%')).toBeInTheDocument();
    expect(within(p95).getByText('+43.0% … +55.0%')).toBeInTheDocument();
    expect(within(p95).getByText('±7%')).toBeInTheDocument();
    expect(within(p95).getByText('regression')).toBeInTheDocument();
    const errs = within(tables[0]!).getByText('error rate').closest('tr')!;
    expect(within(errs).getByText('+∞')).toBeInTheDocument();
    expect(within(tables[1]!).getByText('POST /api/checkout')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '11111111' })).toHaveAttribute(
      'href',
      '/runs/11111111-0000-4000-8000-000000000000',
    );

    await user.click(screen.getByRole('button', { name: 'Copy Markdown' }));
    expect(await navigator.clipboard.readText()).toBe(c.markdown);
  });
});

describe('Comparing runs from the Runs page', () => {
  it('selects finished runs, assigns them to A and B and opens the comparison', async () => {
    const db = installMockApi();
    db.meId = db.users.find((u) => u.role === 'viewer')!.id;
    const project = db.projects[0]!;
    const bodies: CompareRequest[] = [];
    server.events.on('request:start', ({ request }) => {
      if (request.method === 'POST' && request.url.endsWith('/compare'))
        void request
          .clone()
          .json()
          .then((b) => bodies.push(b as CompareRequest));
    });
    const user = userEvent.setup();
    const { router } = renderApp(`/projects/${project.id}/runs`);
    expect(await screen.findByText('Select finished runs to compare them.')).toBeInTheDocument();

    // The run in progress cannot be selected.
    const live = db.runs.find((r) => r.status === 'running')!;
    expect(
      await screen.findByRole('checkbox', {
        name: new RegExp(`Select run ${live.id.slice(0, 8)}`),
      }),
    ).toBeDisabled();

    const smoke = db.runs.filter(
      (r) =>
        r.scenarioName === 'shop-smoke' &&
        r.status === 'completed' &&
        r.targetURL?.includes('staging'),
    );
    const picks = smoke.slice(0, 4);
    for (const r of picks)
      await user.click(
        screen.getByRole('checkbox', { name: new RegExp(`Select run ${r.id.slice(0, 8)}`) }),
      );
    expect(screen.getByRole('status')).toHaveTextContent('4 runs selected');
    await user.click(screen.getByRole('button', { name: /Compare…/ }));

    const dialog = await screen.findByRole('dialog', { name: 'Compare runs' });
    // One scenario version: the older half is A.
    const byAge = [...picks].sort((x, y) => Date.parse(x.createdAt) - Date.parse(y.createdAt));
    expect(
      within(dialog).getByRole('radio', { name: `Run ${byAge[0]!.id.slice(0, 8)} in A` }),
    ).toBeChecked();
    expect(
      within(dialog).getByRole('radio', { name: `Run ${byAge[3]!.id.slice(0, 8)} in B` }),
    ).toBeChecked();
    // Move one more run to B and rename the sides.
    await user.click(
      within(dialog).getByRole('radio', { name: `Run ${byAge[1]!.id.slice(0, 8)} in B` }),
    );
    await user.clear(within(dialog).getByLabelText('Name for A'));
    await user.type(within(dialog).getByLabelText('Name for A'), 'before');
    await user.clear(within(dialog).getByLabelText('Name for B'));
    await user.type(within(dialog).getByLabelText('Name for B'), 'after');
    await user.click(within(dialog).getByRole('button', { name: 'Compare' }));

    await waitFor(() =>
      expect(router.state.location.pathname).toBe(`/projects/${project.id}/compare`),
    );
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toEqual({
      a: [byAge[0]!.id],
      b: [byAge[1]!.id, byAge[2]!.id, byAge[3]!.id],
      labelA: 'before',
      labelB: 'after',
    });
    expect(await screen.findByRole('heading', { name: 'Compare runs' })).toBeInTheDocument();
    const verdict = await screen.findByLabelText('Verdict');
    expect(verdict).toHaveTextContent('before → after');
    expect(screen.getAllByRole('columnheader', { name: 'before' }).length).toBeGreaterThan(0);
    expect(screen.getAllByText('p95').length).toBeGreaterThan(0);
    expect(screen.getByText('Per step')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Copy Markdown' })).toBeInTheDocument();
  });

  it('explains why runs cannot be compared', async () => {
    const db = installMockApi();
    const project = db.projects[0]!;
    const live = db.runs.find((r) => r.status === 'running')!;
    const done = db.runs.find((r) => r.status === 'completed')!;
    renderApp(`/projects/${project.id}/compare?a=${done.id}&b=${live.id}`);
    expect(await screen.findByText(/every run must be in your organisation/)).toBeInTheDocument();
    expect(screen.getByText(/has not finished \(running\)/)).toBeInTheDocument();
  });

  it('asks for runs when none are given', async () => {
    const db = installMockApi();
    renderApp(`/projects/${db.projects[0]!.id}/compare`);
    expect(await screen.findByText('Choose runs to compare')).toBeInTheDocument();
  });
});
