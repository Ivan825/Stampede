import { expect, test, type Page } from '@playwright/test';
import { nav, open } from './fixtures';

/** Opens the live checkout-stress run from the runs list. */
async function openLiveRun(page: Page) {
  await nav(page, 'Runs');
  await page
    .getByRole('row')
    .filter({ hasText: 'checkout-stress' })
    .filter({ hasText: 'running' })
    .getByRole('link')
    .first()
    .click();
  await expect(page).toHaveURL(/\/runs\/[0-9a-f-]{36}$/);
}

test('limits show the organisation’s caps read-only, with the command that sets them', async ({
  page,
}) => {
  await open(page, '/settings?tab=limits');
  const org = page.getByRole('table', { name: 'Organisation caps' });
  await expect(org).toContainText('3,000');
  await expect(page.getByRole('form', { name: 'Organisation caps' })).toHaveCount(0);
  await expect(
    page.getByText('stampede caps set --max-rate <n> --max-vus <n> --max-duration <d>', {
      exact: true,
    }),
  ).toBeVisible();

  const store = page
    .getByRole('table', { name: 'Project caps' })
    .getByRole('row')
    .filter({ hasText: 'Storefront' });
  await expect(store).toContainText('required');
  await store.getByRole('link', { name: 'Storefront' }).click();
  await expect(page.getByRole('heading', { name: 'Project settings' })).toBeVisible();
});

test('project settings show caps, the dry-run gate and roles read-only, even to admins', async ({
  page,
}) => {
  await open(page, '/');
  await nav(page, 'Project settings');
  const settings = page.getByRole('region', { name: 'Caps and dry run' });
  await expect(settings).toContainText('required');
  await expect(settings).toContainText(/stampede projects settings set --project \S+/);
  await expect(page.getByRole('form')).toHaveCount(0);
  await expect(page.getByRole('checkbox')).toHaveCount(0);
  const members = page.getByRole('table', { name: 'Project members' });
  await expect(members.getByRole('combobox')).toHaveCount(0);
  await expect(members.getByRole('row').filter({ hasText: 'Kenji Mori' })).toContainText(
    'Project override',
  );
  await expect(
    page.getByText(/stampede projects roles set <email> <role> --project/),
  ).toBeVisible();
});

test('schedules and drift results are read-only and point to the CLI', async ({ page }) => {
  await open(page, '/');
  await nav(page, 'Schedules');
  await expect(page.getByRole('button', { name: /New schedule|Run now|Check now/ })).toHaveCount(0);
  await expect(page.getByText(/^stampede schedules create /)).toBeVisible();
  await expect(page.getByText('stampede schedules run <name>')).toBeVisible();
  const schedules = page.getByRole('table', { name: 'Schedules' });
  await expect(schedules.getByRole('switch')).toHaveCount(0);

  // The seeded hourly check found a broken journey.
  await schedules
    .getByRole('button', { name: 'View the last drift check of hourly-drift' })
    .click();
  const check = page.getByRole('dialog', { name: 'Drift check: hourly-drift' });
  await expect(check.getByText('The journey login no longer works against the API.')).toBeVisible();
  await expect(check.getByRole('group', { name: 'Journey login' })).toContainText(
    'status 200 expected, got 404',
  );
  await expect(check.getByRole('list', { name: 'Removed endpoints' })).toContainText(
    'POST /api/login',
  );
  await expect(check.getByText(/^stampede drift repair [0-9a-f-]{36}$/)).toBeVisible();
  await expect(check.getByRole('button', { name: 'Propose a fix' })).toHaveCount(0);
});

test('targets and secrets are listed read-only', async ({ page }) => {
  await open(page, '/');
  await nav(page, 'Targets');
  await expect(page.getByRole('heading', { name: 'Targets' })).toBeVisible();
  await expect(page.getByText('https://staging.shop.acme.dev', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: /New target|Check now|Delete|Edit/ })).toHaveCount(
    0,
  );
  await expect(page.getByText(/^stampede targets create /)).toBeVisible();
  await nav(page, 'Secrets');
  await expect(page.getByRole('heading', { name: 'Secrets' })).toBeVisible();
  await expect(page.getByRole('table', { name: 'Secrets' })).toBeVisible();
  await expect(page.getByRole('button', { name: /New secret|Update|Delete/ })).toHaveCount(0);
  await expect(page.getByText(/^stampede secrets set <NAME> --project/)).toBeVisible();
});

test('a run the dry-run gate refused explains which journey failed', async ({ page }) => {
  await open(page, '/');
  await nav(page, 'Runs');
  await page
    .getByRole('row')
    .filter({ hasText: 'release 2026.10.3 smoke' })
    .getByRole('link')
    .first()
    .click();
  await expect(page).toHaveURL(/\/runs\/[0-9a-f-]{36}$/);
  const gate = page.getByRole('region', { name: 'Dry run before load' });
  await expect(gate).toContainText('1 of 2 journeys failed the dry run; no load was started.');
  const login = gate.getByRole('row').filter({ hasText: 'login' });
  await expect(login).toContainText('FAILED');
  await expect(login).toContainText('status 200 expected, got 404');
  await expect(page.getByRole('list', { name: 'Run events' })).toContainText(
    'journey browse passed its dry run',
  );
});

test('the live view shows the dry run that passed before load', async ({ page }) => {
  await open(page, '/');
  await openLiveRun(page);
  const gate = page.getByRole('region', { name: 'Dry run before load' });
  await expect(gate).toContainText('PASSED');
  await expect(gate).toContainText(/All \d+ journeys? passed the dry run; starting load\./);
  await expect(page.getByRole('list', { name: 'Run events' })).toContainText('dryrun.passed');
});

test('the Kill safety control stops a live run', async ({ page }) => {
  await open(page, '/');
  await openLiveRun(page);
  const safety = page.getByRole('group', { name: 'Safety controls' });
  await expect(safety.getByRole('button', { name: 'Stop' })).toBeVisible();
  await safety.getByRole('button', { name: 'Kill switch: kill this run' }).click();
  const confirm = page.getByRole('alertdialog', { name: 'Kill this run?' });
  await confirm.getByRole('button', { name: 'Kill run' }).click();
  await expect(page.getByText('Run killed.')).toBeVisible();
  await expect(page.getByRole('group', { name: 'Safety controls' })).toHaveCount(0, {
    timeout: 20_000,
  });
  await expect(page.getByRole('heading', { level: 1 })).toContainText('aborted');
});

test('the header kill switch stops every active run', async ({ page }) => {
  await open(page, '/');
  const killAll = page.getByRole('button', { name: /Kill switch: stop all \d+ active runs?/ });
  await killAll.click();
  const confirm = page.getByRole('alertdialog', { name: 'Kill switch: stop all load now?' });
  await expect(confirm).toContainText('stampede kill --all');
  await confirm.getByRole('button', { name: /^Kill \d+ active runs?$/ }).click();
  await expect(page.getByText(/^Killed \d+ runs?\.$/)).toBeVisible();
  await expect(killAll).toHaveCount(0, { timeout: 20_000 });
});

test('viewers see no safety controls', async ({ page }) => {
  await open(page, '/?mock=viewer');
  await openLiveRun(page);
  await expect(page.getByRole('list', { name: 'Worker health' })).toBeVisible();
  await expect(page.getByRole('group', { name: 'Safety controls' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: /Kill switch/ })).toBeDisabled();
});
