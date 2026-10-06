import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { server } from '@/test/server';
import { api, ApiError, errorLines, unwrap } from './client';

describe('ApiError.from', () => {
  it('reads the error envelope with details', () => {
    const e = ApiError.from(422, {
      error: { code: 'invalid', message: 'The run is not valid.', details: ['rate: bad', 7] },
    });
    expect(e).toBeInstanceOf(ApiError);
    expect(e.status).toBe(422);
    expect(e.code).toBe('invalid');
    expect(e.message).toBe('The run is not valid.');
    expect(e.details).toEqual(['rate: bad']);
  });

  it('falls back to the text body or status for other errors', () => {
    expect(ApiError.from(502, 'Bad gateway from proxy').message).toBe('Bad gateway from proxy');
    const e = ApiError.from(500, {}, 'Internal Server Error');
    expect(e.code).toBe('http_500');
    expect(e.message).toBe('Internal Server Error');
    expect(ApiError.from(503, undefined).message).toBe('Request failed with status 503');
  });
});

describe('unwrap', () => {
  it('returns data on success and sends the CSRF header', async () => {
    let csrf: string | null = null;
    server.use(
      http.post('*/api/v1/projects', ({ request }) => {
        csrf = request.headers.get('X-Stampede-CSRF');
        return HttpResponse.json(
          { id: 'p1', name: 'Shop', slug: 'shop', createdAt: '2026-10-06T00:00:00Z' },
          { status: 201 },
        );
      }),
    );
    const p = await unwrap(api.POST('/projects', { body: { name: 'Shop' } }));
    expect(p.slug).toBe('shop');
    expect(csrf).toBe('1');
  });

  it('throws ApiError with message and details for error responses', async () => {
    server.use(
      http.post('*/api/v1/projects', () =>
        HttpResponse.json(
          { error: { code: 'invalid', message: 'Name is taken.', details: ['name: exists'] } },
          { status: 409 },
        ),
      ),
    );
    const err = await unwrap(api.POST('/projects', { body: { name: 'Shop' } })).catch(
      (e: unknown) => e,
    );
    expect(err).toBeInstanceOf(ApiError);
    expect(errorLines(err)).toEqual({ message: 'Name is taken.', details: ['name: exists'] });
    expect((err as ApiError).status).toBe(409);
  });

  it('turns network failures into a readable ApiError', async () => {
    server.use(http.get('*/api/v1/projects', () => HttpResponse.error()));
    const err = await unwrap(api.GET('/projects')).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe('network');
    expect((err as ApiError).message).toMatch(/Could not reach the Stampede server/);
  });

  it('returns undefined for 204 responses', async () => {
    server.use(http.post('*/api/v1/auth/logout', () => new HttpResponse(null, { status: 204 })));
    await expect(unwrap(api.POST('/auth/logout'))).resolves.toBeUndefined();
  });
});

describe('errorLines', () => {
  it('handles plain errors and unknown values', () => {
    expect(errorLines(new Error('boom'))).toEqual({ message: 'boom', details: [] });
    expect(errorLines('x').message).toBe('Something went wrong.');
  });
});
