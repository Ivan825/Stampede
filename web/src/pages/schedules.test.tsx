import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import type { ScheduleCreate } from '@/api/types';
import { nextTimes, parseCron } from '@/mocks/cron';
import { renderApp } from '@/test/render';
import { installMockApi, server } from '@/test/server';

function setup(role?: 'viewer' | 'runner') {
  const db = installMockApi();
  if (role) db.meId = db.users.find((u) => u.role === role)!.id;
  const project = db.projects[0]!;
  const user = userEvent.setup();
  const utils = renderApp(`/projects/${project.id}/schedules`);
  return { db, project, user, ...utils };
}

async function row(name: string) {
  const cell = await screen.findByText(name);
  return cell.closest('tr')!;
}

describe('SchedulesPage', () => {
  it('lists schedules with their next run, last verdict and state', async () => {
    setup();
    expect(await screen.findByRole('heading', { name: 'Schedules' })).toBeInTheDocument();
    const nightly = await row('nightly-smoke');
    expect(within(nightly).getByText('0 2 * * *')).toBeInTheDocument();
    expect(within(nightly).getByText('UTC')).toBeInTheDocument();
    expect(within(nightly).getByText(/02:00 UTC/)).toBeInTheDocument();
    expect(within(nightly).getByText('PASS')).toBeInTheDocument();
    expect(within(nightly).getByRole('switch', { name: 'Disable nightly-smoke' })).toBeChecked();

    const weekday = await row('weekday-stress');
    expect(within(weekday).getByText('Disabled')).toBeInTheDocument();
    expect(within(weekday).getByText('Europe/London')).toBeInTheDocument();
    expect(
      within(weekday).getByRole('switch', { name: 'Enable weekday-stress' }),
    ).not.toBeChecked();
  });

  it('is read-only for viewers', async () => {
    setup('viewer');
    const nightly = await row('nightly-smoke');
    expect(within(nightly).getByText('On')).toBeInTheDocument();
    expect(within(nightly).queryByRole('switch')).not.toBeInTheDocument();
    expect(within(nightly).queryByRole('button')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /New schedule/ })).not.toBeInTheDocument();
  });

  it('lets runners start a schedule now but not change it', async () => {
    const { user, router, db } = setup('runner');
    const nightly = await row('nightly-smoke');
    expect(within(nightly).queryByRole('switch')).not.toBeInTheDocument();
    expect(within(nightly).queryByRole('button', { name: /Edit/ })).not.toBeInTheDocument();
    await user.click(within(nightly).getByRole('button', { name: 'Run nightly-smoke now' }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/runs\/[0-9a-f-]+$/));
    const run = db.runs.find((r) => r.id === router.state.location.pathname.split('/')[2]);
    expect(run?.note).toBe('scheduled: nightly-smoke');
    expect(db.schedules.find((s) => s.name === 'nightly-smoke')?.lastRunId).toBe(run?.id);
  });

  it('disables and enables a schedule', async () => {
    const { user, db } = setup();
    const nightly = await row('nightly-smoke');
    await user.click(within(nightly).getByRole('switch', { name: 'Disable nightly-smoke' }));
    await waitFor(() => expect(within(nightly).getByText('Disabled')).toBeInTheDocument());
    expect(db.schedules.find((s) => s.name === 'nightly-smoke')?.enabled).toBe(false);
    await user.click(within(nightly).getByRole('switch', { name: 'Enable nightly-smoke' }));
    await waitFor(() => expect(within(nightly).queryByText('Disabled')).not.toBeInTheDocument());
  });

  it('previews the next three runs and creates a schedule', async () => {
    const { user, db, project } = setup();
    const bodies: ScheduleCreate[] = [];
    server.events.on('request:start', ({ request }) => {
      if (request.method === 'POST' && request.url.endsWith(`/projects/${project.id}/schedules`))
        void request
          .clone()
          .json()
          .then((b) => bodies.push(b as ScheduleCreate));
    });
    await user.click(await screen.findByRole('button', { name: /New schedule/ }));
    const dialog = await screen.findByRole('dialog', { name: 'New schedule' });

    // The default preview, then an invalid expression is explained.
    const preview = await within(dialog).findByRole('status', { name: 'Next runs' });
    expect(within(preview).getAllByText(/02:00 UTC/)).toHaveLength(3);
    const cron = within(dialog).getByLabelText('Cron');
    await user.clear(cron);
    await user.type(cron, '61 * * * *');
    expect(await within(dialog).findByText(/minute: 61 is out of range/)).toBeInTheDocument();

    await user.clear(cron);
    await user.click(within(dialog).getByRole('button', { name: 'Weekdays 06:30' }));
    expect(cron).toHaveValue('30 6 * * MON-FRI');
    const tz = within(dialog).getByLabelText('Time zone');
    await user.clear(tz);
    await user.type(tz, 'Asia/Kolkata');
    await waitFor(() => expect(within(dialog).getAllByText(/06:30 GMT\+5:30/)).toHaveLength(3));

    await user.type(within(dialog).getByLabelText('Name'), 'morning-check');
    await user.selectOptions(within(dialog).getByLabelText('Scenario'), 'checkout-stress');
    await user.selectOptions(
      within(dialog).getByLabelText('Target'),
      within(dialog).getByRole('option', { name: /staging/ }),
    );
    await user.click(within(dialog).getByRole('button', { name: /Load overrides/ }));
    await user.type(within(dialog).getByLabelText('Duration'), '2m');
    await user.click(within(dialog).getByRole('button', { name: 'Create schedule' }));

    await waitFor(() => expect(bodies).toHaveLength(1));
    const stress = db.scenarios.find((s) => s.name === 'checkout-stress')!;
    const staging = db.targets.find((t) => t.name === 'staging')!;
    expect(bodies[0]).toEqual({
      name: 'morning-check',
      scenarioId: stress.id,
      targetId: staging.id,
      cron: '30 6 * * MON-FRI',
      timezone: 'Asia/Kolkata',
      overrides: { duration: '2m' },
      env: {},
      workers: 0,
      enabled: true,
      note: '',
    });
    expect(await row('morning-check')).toBeInTheDocument();
  });

  it('deletes a schedule after confirmation', async () => {
    const { user, db } = setup();
    const weekday = await row('weekday-stress');
    await user.click(within(weekday).getByRole('button', { name: 'Delete weekday-stress' }));
    const confirm = await screen.findByRole('alertdialog');
    await user.click(within(confirm).getByRole('button', { name: 'Delete schedule' }));
    await waitFor(() => expect(screen.queryByText('weekday-stress')).not.toBeInTheDocument());
    expect(db.schedules.map((s) => s.name)).toEqual(['nightly-smoke', 'hourly-drift']);
  });
});

describe('mock cron', () => {
  it('finds weekday and zoned firings', () => {
    const after = new Date('2026-10-09T12:00:00Z'); // a Friday
    expect(nextTimes(parseCron('30 6 * * MON-FRI'), 'UTC', after, 2)).toEqual([
      '2026-10-12T06:30:00.000Z',
      '2026-10-13T06:30:00.000Z',
    ]);
    expect(nextTimes(parseCron('@daily'), 'Asia/Kolkata', after, 1)).toEqual([
      '2026-10-09T18:30:00.000Z',
    ]);
    expect(() => parseCron('* * *')).toThrow('5 fields');
  });
});
