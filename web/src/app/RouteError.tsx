import { Link, type ErrorComponentProps } from '@tanstack/react-router';
import { ErrorAlert } from '@/components/ui';

export function RouteError({ error, reset }: ErrorComponentProps) {
  return (
    <div className="mx-auto max-w-xl p-8">
      <h1 className="mb-3 text-lg font-semibold">This page could not load</h1>
      <ErrorAlert error={error} />
      <div className="mt-4 flex gap-3 text-[13px]">
        <button type="button" className="text-info underline" onClick={reset}>
          Try again
        </button>
        <Link to="/" className="text-info underline">
          Go home
        </Link>
      </div>
    </div>
  );
}

export function NotFound() {
  return (
    <div className="mx-auto max-w-xl p-8">
      <h1 className="text-lg font-semibold">Not found</h1>
      <p className="mt-1 text-[13px] text-muted">There is nothing at this address.</p>
      <Link to="/" className="mt-4 inline-block text-[13px] text-info underline">
        Go home
      </Link>
    </div>
  );
}
