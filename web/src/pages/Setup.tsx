import { useNavigate } from '@tanstack/react-router';
import { useState, type FormEvent } from 'react';
import { useSetup } from '@/api/queries';
import { Button, ErrorAlert, Field, Input } from '@/components/ui';
import { AuthLayout } from './AuthLayout';

export const MIN_PASSWORD = 10;

export function SetupPage() {
  const navigate = useNavigate();
  const setup = useSetup();
  const [form, setForm] = useState({ organisation: '', name: '', email: '', password: '' });
  const [touched, setTouched] = useState(false);

  const set = (k: keyof typeof form) => (e: { target: { value: string } }) =>
    setForm((f) => ({ ...f, [k]: e.target.value }));

  const errors = {
    organisation: form.organisation.trim() ? undefined : 'Enter an organisation name.',
    name: form.name.trim() ? undefined : 'Enter your name.',
    email: /^[^\s@]+@[^\s@]+$/.test(form.email.trim()) ? undefined : 'Enter a valid email address.',
    password:
      form.password.length >= MIN_PASSWORD ? undefined : `Use at least ${MIN_PASSWORD} characters.`,
  };
  const valid = Object.values(errors).every((e) => !e);
  const show = (k: keyof typeof errors) => (touched ? errors[k] : undefined);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (!valid) return;
    setup.mutate(
      {
        organisation: form.organisation.trim(),
        name: form.name.trim(),
        email: form.email.trim(),
        password: form.password,
      },
      { onSuccess: () => void navigate({ to: '/projects' }) },
    );
  };

  return (
    <AuthLayout
      title="Set up Stampede"
      subtitle="Create your organisation and the owner account. You can invite others afterwards."
    >
      <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <Field label="Organisation" error={show('organisation')}>
          {(p) => (
            <Input {...p} autoFocus value={form.organisation} onChange={set('organisation')} />
          )}
        </Field>
        <Field label="Your name" error={show('name')}>
          {(p) => <Input {...p} autoComplete="name" value={form.name} onChange={set('name')} />}
        </Field>
        <Field label="Email" error={show('email')}>
          {(p) => (
            <Input
              {...p}
              type="email"
              autoComplete="username"
              value={form.email}
              onChange={set('email')}
            />
          )}
        </Field>
        <Field
          label="Password"
          hint={`At least ${MIN_PASSWORD} characters.`}
          error={show('password')}
        >
          {(p) => (
            <Input
              {...p}
              type="password"
              autoComplete="new-password"
              value={form.password}
              onChange={set('password')}
            />
          )}
        </Field>
        <ErrorAlert error={setup.error} />
        <Button type="submit" variant="primary" loading={setup.isPending}>
          Create owner account
        </Button>
      </form>
    </AuthLayout>
  );
}
