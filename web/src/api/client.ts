import createClient, { type Middleware } from 'openapi-fetch';
import type { paths } from './schema.gen';

export const API_BASE = '/api/v1';

/**
 * An error returned by the API ({"error":{"code","message","details"}}) or a
 * failure to reach it at all (code "network").
 */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: string[];

  constructor(status: number, code: string, message: string, details: string[] = []) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.details = details;
  }

  /** Builds an ApiError from a response status and its (possibly non-JSON) body. */
  static from(status: number, body: unknown, statusText = ''): ApiError {
    if (isErrorBody(body)) {
      const e = body.error;
      const details = Array.isArray(e.details)
        ? e.details.filter((d): d is string => typeof d === 'string')
        : [];
      return new ApiError(status, e.code, e.message, details);
    }
    const text = typeof body === 'string' && body.trim() ? body.trim().slice(0, 300) : '';
    return new ApiError(
      status,
      `http_${status}`,
      text || statusText || `Request failed with status ${status}`,
    );
  }
}

function isErrorBody(
  body: unknown,
): body is { error: { code: string; message: string; details?: unknown } } {
  if (typeof body !== 'object' || body === null || !('error' in body)) return false;
  const e = body.error;
  return (
    typeof e === 'object' &&
    e !== null &&
    typeof (e as { code?: unknown }).code === 'string' &&
    typeof (e as { message?: unknown }).message === 'string'
  );
}

/** Messages to show for an unknown error value. */
export function errorLines(err: unknown): { message: string; details: string[] } {
  if (err instanceof ApiError) return { message: err.message, details: err.details };
  if (err instanceof Error) return { message: err.message, details: [] };
  return { message: 'Something went wrong.', details: [] };
}

type UnauthorizedListener = () => void;
const unauthorizedListeners = new Set<UnauthorizedListener>();

/** Called whenever an authenticated request comes back 401. */
export function onUnauthorized(listener: UnauthorizedListener): () => void {
  unauthorizedListeners.add(listener);
  return () => unauthorizedListeners.delete(listener);
}

const authMiddleware: Middleware = {
  onResponse({ response, request }) {
    const path = new URL(request.url).pathname;
    const isAuthCall = path.endsWith('/auth/login') || path.endsWith('/me');
    if (response.status === 401 && !isAuthCall) {
      unauthorizedListeners.forEach((l) => l());
    }
    return undefined;
  },
};

function origin(): string {
  return typeof window !== 'undefined' ? window.location.origin : 'http://localhost';
}

/**
 * The typed API client. Every request sends the CSRF header: the server
 * requires it on state-changing requests that use the session cookie.
 */
export const api = createClient<paths>({
  baseUrl: `${origin()}${API_BASE}`,
  credentials: 'same-origin',
  headers: { 'X-Stampede-CSRF': '1' },
  // Resolve fetch at call time so test interceptors and mocks apply.
  fetch: (input: Request) => globalThis.fetch(input),
});
api.use(authMiddleware);

interface FetchResult<T> {
  data?: T;
  error?: unknown;
  response: Response;
}

/**
 * Awaits an openapi-fetch call and returns its data, throwing ApiError for
 * error responses and network failures.
 */
export async function unwrap<T>(call: Promise<FetchResult<T>>): Promise<T> {
  let result: FetchResult<T>;
  try {
    result = await call;
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    throw new ApiError(0, 'network', `Could not reach the Stampede server (${msg}).`);
  }
  const { data, error, response } = result;
  if (!response.ok || error !== undefined) {
    throw ApiError.from(response.status, error, response.statusText);
  }
  return data as T;
}

/** URL of a run report download in the given format. */
export function reportUrl(runId: string, format: 'html' | 'json' | 'junit' | 'markdown'): string {
  return `${API_BASE}/runs/${encodeURIComponent(runId)}/report?format=${format}`;
}

/** URL of a run's live event stream. */
export function liveUrl(runId: string): string {
  return `${API_BASE}/runs/${encodeURIComponent(runId)}/live`;
}
