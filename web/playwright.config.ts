import { defineConfig, devices } from '@playwright/test';

/**
 * End-to-end tests of the web UI in a real browser, against the dev build
 * with the mock API (`pnpm dev:mock`). The mock API lives in the page, so a
 * full page load starts from fresh fixtures: each test loads one URL and
 * then navigates inside the app.
 */
const port = Number(process.env.E2E_PORT ?? 5179);

export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 2,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  // A production build in mock mode: the dev server would optimise
  // dependencies on first use and reload the page, losing the mock data.
  webServer: {
    command: `pnpm exec vite build --mode mock --outDir .e2e-dist --logLevel warn && pnpm exec vite preview --mode mock --outDir .e2e-dist --host 127.0.0.1 --port ${port} --strictPort`,
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
