import { expect, type Page } from '@playwright/test';

/** Opens a URL and waits for the app shell (signed in) to render. */
export async function open(page: Page, url = '/') {
  await page.goto(url);
  await expect(page.getByRole('navigation', { name: 'Main' })).toBeVisible();
}

/** Follows a link in the main navigation. */
export async function nav(page: Page, name: string) {
  await page
    .getByRole('navigation', { name: 'Main' })
    .getByRole('link', { name, exact: true })
    .click();
}
