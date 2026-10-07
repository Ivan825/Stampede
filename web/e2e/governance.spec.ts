import { expect, test } from '@playwright/test';
import { nav, open } from './fixtures';

test('admins set the organisation’s caps and see them in the effective limits', async ({
  page,
}) => {
  await open(page, '/settings?tab=limits');
  const form = page.getByRole('form', { name: 'Organisation caps' });
  await expect(form.getByLabel('Max VUs')).toHaveValue('3000');
  await form.getByLabel('Max rate (/s)').fill('800');
  await form.getByLabel('Max duration (s)').fill('1800');
  await form.getByRole('button', { name: 'Save caps' }).click();
  await expect(page.getByText('Organisation caps saved.')).toBeVisible();

  const staging = page
    .getByRole('table', { name: 'Target caps' })
    .getByRole('row')
    .filter({ hasText: 'https://staging.shop.acme.dev' });
  await expect(staging).toContainText('800/s');
  await expect(staging).toContainText('30m');

  const store = page
    .getByRole('table', { name: 'Project caps' })
    .getByRole('row')
    .filter({ hasText: 'Storefront' });
  await expect(store).toContainText('required');
  await store.getByRole('link', { name: 'Storefront' }).click();
  await expect(page.getByRole('heading', { name: 'Project settings' })).toBeVisible();
});

test('project admins change caps, the dry-run gate and members’ roles', async ({ page }) => {
  await open(page, '/');
  await nav(page, 'Project settings');
  await expect(page.getByRole('heading', { name: 'Project settings' })).toBeVisible();

  const form = page.getByRole('form', { name: 'Caps and dry run' });
  const gate = form.getByRole('checkbox', { name: 'Require a passing dry run before load' });
  await expect(gate).toBeChecked();
  await form.getByLabel('Max rate (/s)').fill('250');
  await gate.uncheck();
  await form.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByText('Project settings saved.')).toBeVisible();
  await expect(gate).not.toBeChecked();
  await expect(form.getByLabel('Max rate (/s)')).toHaveValue('250');

  // The Limits tab shows the saved values.
  await page.getByRole('button', { name: /Account menu/ }).click();
  await page.getByRole('menuitem', { name: 'Settings' }).click();
  await page.getByRole('tab', { name: 'Limits' }).click();
  const store = page
    .getByRole('table', { name: 'Project caps' })
    .getByRole('row')
    .filter({ hasText: 'Storefront' });
  await expect(store).toContainText('not required');
  await expect(store).toContainText('250/s');
  await nav(page, 'Project settings');

  const members = page.getByRole('table', { name: 'Project members' });
  const ana = members.getByRole('combobox', { name: 'Role for Ana Lima in this project' });
  await ana.selectOption('viewer');
  await expect(page.getByText('Ana Lima is now viewer in this project.')).toBeVisible();
  await members.getByRole('button', { name: 'Remove the project override for Ana Lima' }).click();
  await expect(
    page.getByText('Ana Lima has their organisation role (editor) here again.'),
  ).toBeVisible();
  await expect(ana).toHaveValue('');
});

test('project settings are read-only for viewers', async ({ page }) => {
  await open(page, '/?mock=viewer');
  await nav(page, 'Project settings');
  const settings = page.getByRole('region', { name: 'Caps and dry run' });
  await expect(settings).toContainText('Read-only');
  await expect(settings).toContainText('required');
  await expect(page.getByRole('form', { name: 'Caps and dry run' })).toHaveCount(0);
  await expect(
    page.getByRole('table', { name: 'Project members' }).getByRole('combobox'),
  ).toHaveCount(0);
});

test('a drift schedule shows what broke and proposes a fix for review', async ({ page }) => {
  await open(page, '/');
  await nav(page, 'Schedules');

  // A new drift check, with the spec on the target's host.
  await page.getByRole('button', { name: 'New schedule' }).click();
  const dialog = page.getByRole('dialog', { name: 'New schedule' });
  await dialog.getByRole('radio', { name: /Check for drift/ }).check();
  await dialog.getByLabel('Name').fill('api-drift');
  await dialog
    .getByLabel('Target')
    .selectOption({ label: 'staging — https://staging.shop.acme.dev' });
  await dialog.getByLabel('OpenAPI spec URL').fill('https://staging.shop.acme.dev/openapi.yaml');
  await dialog.getByRole('button', { name: 'Create schedule' }).click();
  const schedules = page.getByRole('table', { name: 'Schedules' });
  await expect(schedules.getByRole('row').filter({ hasText: 'api-drift' })).toContainText(
    'drift check',
  );

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
  await check.getByRole('button', { name: 'Propose a fix' }).click();
  await expect(page).toHaveURL(/\/projects\/[0-9a-f-]{36}\/ai\/[0-9a-f-]{36}$/);
  await expect(page.getByRole('heading', { name: /^Job [0-9a-f]{8}/ })).toBeVisible();
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
  await nav(page, 'Runs');
  await page
    .getByRole('row')
    .filter({ hasText: 'checkout-stress' })
    .filter({ hasText: 'running' })
    .getByRole('link')
    .first()
    .click();
  const gate = page.getByRole('region', { name: 'Dry run before load' });
  await expect(gate).toContainText('PASSED');
  await expect(gate).toContainText(/All \d+ journeys? passed the dry run; starting load\./);
  await expect(page.getByRole('list', { name: 'Run events' })).toContainText('dryrun.passed');
});
