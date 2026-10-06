import { setupServer } from 'msw/node';
import { createHandlers } from '@/mocks/handlers';
import type { MockOptions } from '@/mocks/db';

export const server = setupServer();

/** Installs a fresh mock API for one test and returns its store. */
export function installMockApi(opts: Partial<MockOptions> = {}) {
  const mock = createHandlers({ setupRequired: false, signedIn: true, ...opts });
  server.use(...mock.handlers);
  return mock.getDb();
}
