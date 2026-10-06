import { useNavigate, useSearch } from '@tanstack/react-router';
import { useState, type FormEvent } from 'react';
import { useLogin } from '@/api/queries';
import { Button, ErrorAlert, Field, Input } from '@/components/ui';
import { AuthLayout } from './AuthLayout';

export function LoginPage() {
  const search: { redirect?: string } = useSearch({ strict: false });
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
