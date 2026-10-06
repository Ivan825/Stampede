import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { renderApp } from '@/test/render';
import { installMockApi, server } from '@/test/server';

describe('first-run setup', () => {
  it('redirects to setup while the server has no users', async () => {
    installMockApi({ setupRequired: true, signedIn: false });
    const { router } = renderApp('/');
    expect(await screen.findByRole('heading', { name: 'Set up Stampede' })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe('/setup');
  });

  it('validates the form, then creates the owner and signs in', async () => {
    installMockApi({ setupRequired: true, signedIn: false });
    const user = userEvent.setup();
    const { router } = renderApp('/setup');
    await screen.findByRole('heading', { name: 'Set up Stampede' });

    await user.type(screen.getByLabelText('Organisation'), 'Initech');
    await user.type(screen.getByLabelText('Your name'), 'Peter Gibbons');
    await user.type(screen.getByLabelText('Email'), 'peter@initech.test');
    await user.type(screen.getByLabelText('Password'), 'short');
    await user.click(screen.getByRole('button', { name: 'Create owner account' }));
    expect(screen.getByText('Use at least 10 characters.')).toBeInTheDocument();

    await user.clear(screen.getByLabelText('Password'));
    await user.type(screen.getByLabelText('Password'), 'a-long-enough-password');
    await user.click(screen.getByRole('button', { name: 'Create owner account' }));

    await waitFor(() => expect(router.state.location.pathname).toBe('/projects'));
    expect(
      await screen.findByRole('button', { name: /Account menu for Peter Gibbons/ }),
    ).toBeInTheDocument();
  });

  it('shows the server error message and details', async () => {
    installMockApi({ setupRequired: true, signedIn: false });
    server.use(
      http.post('*/api/v1/setup', () =>
        HttpResponse.json(
          {
            error: {
              code: 'invalid',
              message: 'The setup details are not valid.',
              details: ['email: domain does not accept mail'],
            },
          },
          { status: 422 },
        ),
      ),
    );
    const user = userEvent.setup();
    renderApp('/setup');
    await screen.findByRole('heading', { name: 'Set up Stampede' });
    await user.type(screen.getByLabelText('Organisation'), 'Initech');
    await user.type(screen.getByLabelText('Your name'), 'Peter');
    await user.type(screen.getByLabelText('Email'), 'peter@initech.test');
    await user.type(screen.getByLabelText('Password'), 'a-long-enough-password');
    await user.click(screen.getByRole('button', { name: 'Create owner account' }));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('The setup details are not valid.');
    expect(alert).toHaveTextContent('email: domain does not accept mail');
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
