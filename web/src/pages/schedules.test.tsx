import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { nextTimes, parseCron } from '@/mocks/cron';
import { renderApp } from '@/test/render';
import { installMockApi } from '@/test/server';

function setup(role?: 'viewer' | 'runner') {
  const db = installMockApi();
  if (role) db.meId = db.users.find((u) => u.role === role)!.id;
  const project = db.projects[0]!;
  const user = userEvent.setup();
  const utils = renderApp(`/projects/${project.id}/schedules`);
  return { db, project, user, ...utils };
}

/** A schedule's row; the drift checks table below also names schedules. */
async function row(name: string) {
  const table = await screen.findByRole('table', { name: 'Schedules' });
  const cell = await within(table).findByText(name);
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
    expect(within(nightly).getByText('ON')).toBeInTheDocument();

    const weekday = await row('weekday-stress');
    expect(within(weekday).getByText('Disabled')).toBeInTheDocument();
    expect(within(weekday).getByText('Europe/London')).toBeInTheDocument();
    expect(within(weekday).getByText('OFF')).toBeInTheDocument();
  });

  it('is read-only, pointing to the CLI to create, run and change schedules', async () => {
    setup('runner');
    const nightly = await row('nightly-smoke');
    expect(within(nightly).queryByRole('switch')).not.toBeInTheDocument();
    expect(within(nightly).queryByRole('button')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /New schedule|Run now|Delete/ })).toBeNull();
    expect(
      screen.getByText(
        'stampede schedules create <name> --scenario <scenario> --target <target> --cron "<cron>"',
      ),
    ).toBeInTheDocument();
    expect(screen.getByText('stampede schedules run <name>')).toBeInTheDocument();
    expect(screen.getByText('stampede schedules update <name>')).toBeInTheDocument();
  });

  it('lists drift checks and shows what broke', async () => {
    const { user } = setup('viewer');
    const checks = within(await screen.findByRole('table', { name: 'Drift check results' }));
    const rows = checks.getAllByRole('row').slice(1);
    expect(rows).toHaveLength(2);
    expect(within(rows[0]!).getByText('DRIFTED')).toBeInTheDocument();
    expect(within(rows[0]!).getByText('login')).toBeInTheDocument();
    expect(within(rows[0]!).getByText('3 spec changes')).toBeInTheDocument();
    expect(within(rows[1]!).getByText('NO DRIFT')).toBeInTheDocument();

    await user.click(
      within(await row('hourly-drift')).getByRole('button', {
        name: 'View the last drift check of hourly-drift',
      }),
    );
    const dialog = within(await screen.findByRole('dialog', { name: 'Drift check: hourly-drift' }));
    expect(
      await dialog.findByText('The journey login no longer works against the API.'),
    ).toBeInTheDocument();
    const login = within(dialog.getByRole('group', { name: 'Journey login' }));
    expect(login.getByText('BROKEN')).toBeInTheDocument();
    expect(
      login.getByText('step 1 (POST /api/login): status 200 expected, got 404'),
    ).toBeInTheDocument();
    expect(login.getByText('Pass 1')).toBeInTheDocument();
    expect(
      within(dialog.getByRole('group', { name: 'Journey browse' })).getByText('PASSED'),
    ).toBeInTheDocument();
    expect(
      within(dialog.getByRole('list', { name: 'Removed endpoints' })).getByText('POST /api/login'),
    ).toBeInTheDocument();
    expect(
      within(dialog.getByRole('list', { name: 'Added endpoints' })).getByText(
        'POST /api/v2/sessions',
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog.getByRole('list', { name: 'Requests with no endpoint' })).getByText(
        'login: POST /api/login',
      ),
    ).toBeInTheDocument();
  });

  it('shows the CLI command that proposes a fix instead of a button', async () => {
    const { router, db, project } = setup();
    const latest = db.driftResults[0]!;
    await router.navigate({
      to: '/projects/$projectId/schedules',
      params: { projectId: project.id },
      search: { drift: latest.id },
    });
    const dialog = within(await screen.findByRole('dialog', { name: 'Drift check: hourly-drift' }));
    expect(await dialog.findByText(`stampede drift repair ${latest.id}`)).toBeInTheDocument();
    expect(dialog.queryByRole('button', { name: 'Propose a fix' })).not.toBeInTheDocument();
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
