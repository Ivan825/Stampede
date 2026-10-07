import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { renderApp } from '@/test/render';
import { createDb } from '@/mocks/db';
import { installMockApi } from '@/test/server';

describe('first-run setup', () => {
  it('redirects to setup while the server has no users', async () => {
    installMockApi({ setupRequired: true, signedIn: false });
    const { router } = renderApp('/');
    expect(await screen.findByRole('heading', { name: 'Set up Stampede' })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe('/setup');
  });

  it('explains how to set up the server from the terminal, without a form', async () => {
    installMockApi({ setupRequired: true, signedIn: false });
    renderApp('/setup');
    await screen.findByRole('heading', { name: 'Set up Stampede' });
    expect(screen.getByText('stampede setup')).toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Password')).not.toBeInTheDocument();
  });

  it('checks again and moves on to sign in once the server is set up', async () => {
    const db = installMockApi({ setupRequired: true, signedIn: false });
    const user = userEvent.setup();
    const { router } = renderApp('/setup');
    const go = await screen.findByRole('button', { name: /I have run it, continue/ });
    await user.click(go);
    expect(await screen.findByText(/still needs setting up/)).toBeInTheDocument();
    expect(router.state.location.pathname).toBe('/setup');

    // stampede setup creates the owner.
    db.users.push(createDb({ setupRequired: false, signedIn: false }).users[0]!);
    await user.click(go);
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'));
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument();
  });
});

describe('sign in', () => {
  it('sends signed-out users to the login page and back where they were going', async () => {
    installMockApi({ signedIn: false });
    const user = userEvent.setup();
    const { router } = renderApp('/workers');

    await screen.findByRole('heading', { name: 'Sign in' });
    expect(router.state.location.pathname).toBe('/login');

    await user.type(screen.getByLabelText('Email'), 'priya@acme.dev');
    await user.type(screen.getByLabelText('Password'), 'wrong-password');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The email or password is incorrect.',
    );

    await user.clear(screen.getByLabelText('Password'));
    await user.type(screen.getByLabelText('Password'), 'correct-horse-battery');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    await waitFor(() => expect(router.state.location.pathname).toBe('/workers'));
    expect(await screen.findByRole('heading', { name: 'Workers' })).toBeInTheDocument();
  });

  it('opens the last project when signed in', async () => {
    installMockApi();
    const { router } = renderApp('/');
    expect(await screen.findByRole('heading', { name: 'Storefront' })).toBeInTheDocument();
    expect(router.state.location.pathname).toMatch(/^\/projects\/[0-9a-f-]+$/);
    // The live run makes the kill switch visible.
    expect(
      await screen.findByRole('button', { name: /Kill switch: stop all 1 active run/ }),
    ).toBeInTheDocument();
  });
});
