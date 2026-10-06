import { useNavigate, useSearch } from '@tanstack/react-router';
import { useState, type FormEvent } from 'react';
import { useAuthConfig, useLogin } from '@/api/queries';
import { Button, ErrorAlert, Field, Input, Notice } from '@/components/ui';
import { AuthLayout } from './AuthLayout';

export function LoginPage() {
  const search: { redirect?: string; sso_error?: string } = useSearch({ strict: false });
  const config = useAuthConfig();
  const sso = config.data?.sso;
  const next =
    search.redirect && search.redirect.startsWith('/') && !search.redirect.startsWith('//')
      ? search.redirect
      : '/';
  const navigate = useNavigate();
  const login = useLogin();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');

  const submit = (e: FormEvent) => {
    e.preventDefault();
    login.mutate(
      { email: email.trim(), password },
      {
        onSuccess: () => {
          const to = search.redirect;
          // Only follow same-origin, in-app redirects.
          if (to && to.startsWith('/') && !to.startsWith('//')) {
            void navigate({ href: to });
          } else {
            void navigate({ to: '/' });
          }
        },
      },
    );
  };

  return (
    <AuthLayout title="Sign in">
      {search.sso_error && (
        <Notice tone="warn" className="mb-4">
          Single sign-on failed: {search.sso_error}
        </Notice>
      )}
      {sso && (
        <>
          <a
            href={`${sso.loginURL}?next=${encodeURIComponent(next)}`}
            className="inline-flex h-8 w-full items-center justify-center rounded-md border border-line bg-surface text-sm font-medium hover:border-line-strong hover:bg-surface-2"
          >
            Sign in with {sso.name}
          </a>
          <div className="my-4 flex items-center gap-3 text-xs text-muted" aria-hidden>
            <span className="h-px flex-1 bg-line" />
            or with a password
            <span className="h-px flex-1 bg-line" />
          </div>
        </>
      )}
      <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <Field label="Email">
          {(p) => (
            <Input
              {...p}
              type="email"
              autoComplete="username"
              autoFocus
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          )}
        </Field>
        <Field label="Password">
          {(p) => (
            <Input
              {...p}
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          )}
        </Field>
        <ErrorAlert error={login.error} />
        <Button
          type="submit"
          variant="primary"
          loading={login.isPending}
          disabled={!email || !password}
        >
          Sign in
        </Button>
      </form>
    </AuthLayout>
  );
}
