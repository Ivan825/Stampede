import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router';
import { render } from '@testing-library/react';
import type { ReactNode } from 'react';
import { ToastProvider } from '@/components/toast';
import { makeRouter } from '@/router';

export function testQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } },
  });
}

/** Renders the whole app at a URL, against the mock API installed by the test. */
export function renderApp(url = '/') {
  const queryClient = testQueryClient();
  const history = createMemoryHistory({ initialEntries: [url] });
  const router = makeRouter(queryClient, history);
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  );
  return { ...utils, router, queryClient, history };
}

/**
 * Renders a component inside a minimal router (for components that link or
 * navigate). Other paths render a marker with the current path.
 */
export function renderWithRouter(ui: ReactNode, queryClient = testQueryClient()) {
  const root = createRootRoute();
  const index = createRoute({ getParentRoute: () => root, path: '/', component: () => ui });
  const other = createRoute({
    getParentRoute: () => root,
    path: '$',
    component: function Other() {
      return <p data-testid="location">{router.state.location.pathname}</p>;
    },
  });
  const router = createRouter({
    routeTree: root.addChildren([index, other]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  });
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  );
  return { ...utils, router, queryClient };
}
