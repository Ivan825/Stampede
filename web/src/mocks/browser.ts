import { setupWorker } from 'msw/browser';
import { createHandlers } from './handlers';

/**
 * Starts the mock API in the browser (`pnpm dev:mock`).
 *
 * Signed in as the owner by default. Add `?mock=setup` to the URL to see
 * the first-run setup screen, `?mock=signed-out` for the login screen
 * (sign in as priya@acme.dev / correct-horse-battery) or `?mock=viewer` to
 * see the UI as a read-only user.
 */
export async function startMockWorker() {
  const param = new URLSearchParams(window.location.search).get('mock');
  const { handlers, getDb } = createHandlers(
    { setupRequired: param === 'setup', signedIn: param !== 'signed-out' },
    { latency: 120 },
  );
  if (param === 'viewer') {
    const db = getDb();
    db.meId = db.users.find((u) => u.role === 'viewer')?.id ?? db.meId;
  }
  const worker = setupWorker(...handlers);
  await worker.start({
    onUnhandledRequest: 'bypass',
    quiet: true,
  });
  console.info(
    '[stampede] Mock API active. Sign in as priya@acme.dev / correct-horse-battery when signed out.',
  );
}
