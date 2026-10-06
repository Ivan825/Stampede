import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import type { RunCreate } from '@/api/types';
import { renderWithRouter } from '@/test/render';
import { installMockApi, server } from '@/test/server';
import { NewRunDialog } from './NewRunDialog';

function setup() {
  const db = installMockApi();
  const project = db.projects[0]!;
  const bodies: RunCreate[] = [];
  server.events.on('request:start', ({ request }) => {
    if (request.method === 'POST' && request.url.endsWith(`/projects/${project.id}/runs`)) {
      void request
        .clone()
        .json()
        .then((b) => bodies.push(b as RunCreate));
    }
  });
  const user = userEvent.setup();
  const utils = renderWithRouter(
    <NewRunDialog projectId={project.id} open onOpenChange={() => undefined} />,
  );
  return { db, project, bodies, user, ...utils };
}

describe('NewRunDialog', () => {
  it('defaults to the first scenario and target and shows the plan and caps', async () => {
    setup();
    const dialog = await screen.findByRole('dialog', { name: 'New run' });
    await waitFor(() =>
      expect(within(dialog).getByLabelText('Scenario')).toHaveDisplayValue('shop-smoke'),
    );
    expect(within(dialog).getByLabelText('Target')).toHaveDisplayValue(
      'local — http://localhost:8090',
    );
    expect(await within(dialog).findByText(/Plan: constant-arrival-rate/)).toBeInTheDocument();
    expect(within(dialog).getByText(/Private target — no verification needed/)).toBeInTheDocument();
  });

  it('warns that low caps apply to an unverified public target', async () => {
    const { user } = setup();
    const dialog = await screen.findByRole('dialog', { name: 'New run' });
    const target = within(dialog).getByLabelText('Target');
    await waitFor(() => expect(target).toHaveDisplayValue(/local/));
    await user.selectOptions(target, within(dialog).getByRole('option', { name: /production/ }));
    expect(
      within(dialog).getByText('Unverified public target — low safety caps apply.'),
    ).toBeInTheDocument();
    expect(within(dialog).getByText(/5 iterations\/s · 10 VUs · 1m per run/)).toBeInTheDocument();
  });

  it('validates overrides before submitting', async () => {
    const { user, bodies } = setup();
    const dialog = await screen.findByRole('dialog', { name: 'New run' });
    await waitFor(() =>
      expect(within(dialog).getByLabelText('Scenario')).toHaveDisplayValue('shop-smoke'),
    );
    await user.click(within(dialog).getByRole('button', { name: /Load overrides/ }));
    await user.type(within(dialog).getByLabelText('Rate'), 'fast');
    await user.type(within(dialog).getByLabelText('Virtual users'), '0');
    await user.click(screen.getByRole('button', { name: 'Start run' }));
    expect(within(dialog).getByText('For example 50/s or 3000/m.')).toBeInTheDocument();
    expect(within(dialog).getByText('A whole number, at least 1.')).toBeInTheDocument();
    expect(bodies).toHaveLength(0);
  });

  it('starts a run with overrides, env and note, then opens it', async () => {
    const { user, bodies, router, db } = setup();
    const dialog = await screen.findByRole('dialog', { name: 'New run' });
    const scenario = within(dialog).getByLabelText('Scenario');
    await waitFor(() => expect(scenario).toHaveDisplayValue('shop-smoke'));
    await user.selectOptions(scenario, 'checkout-stress');
    await user.selectOptions(
      within(dialog).getByLabelText('Target'),
      within(dialog).getByRole('option', { name: /staging/ }),
    );

    await user.click(within(dialog).getByRole('button', { name: /Load overrides/ }));
    await user.selectOptions(within(dialog).getByLabelText('Shape'), 'spike');
    await user.selectOptions(within(dialog).getByLabelText('Mode'), 'rate');
    await user.type(within(dialog).getByLabelText('Rate'), '200/s');
    await user.type(within(dialog).getByLabelText('Duration'), '3m');

    await user.click(within(dialog).getByRole('button', { name: 'Add variable' }));
    await user.type(within(dialog).getByLabelText('Variable 1 name'), 'TARGET_URL');
    await user.type(within(dialog).getByLabelText('Variable 1 value'), 'https://x.test');
    await user.clear(within(dialog).getByLabelText('Workers'));
    await user.type(within(dialog).getByLabelText('Workers'), '2');
    await user.type(within(dialog).getByLabelText('Note'), 'spike before launch');

    await user.click(screen.getByRole('button', { name: 'Start run' }));

    await waitFor(() => expect(bodies).toHaveLength(1));
    const stress = db.scenarios.find((s) => s.name === 'checkout-stress')!;
    const staging = db.targets.find((t) => t.name === 'staging')!;
    expect(bodies[0]).toEqual({
      scenarioId: stress.id,
      targetId: staging.id,
      workers: 2,
      overrides: { shape: 'spike', mode: 'rate', rate: '200/s', duration: '3m' },
      env: { TARGET_URL: 'https://x.test' },
      note: 'spike before launch',
    });
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/runs\/[0-9a-f-]+$/));
  });

  it('shows API errors with their details', async () => {
    const { user, project } = setup();
    server.use(
      http.post(`*/api/v1/projects/${project.id}/runs`, () =>
        HttpResponse.json(
          {
            error: {
              code: 'forbidden',
              message: 'Target caps exceeded.',
              details: ['rate 500/s is above the cap of 5/s'],
            },
          },
          { status: 403 },
        ),
      ),
    );
    const dialog = await screen.findByRole('dialog', { name: 'New run' });
    await waitFor(() =>
      expect(within(dialog).getByLabelText('Scenario')).toHaveDisplayValue('shop-smoke'),
    );
    await user.click(screen.getByRole('button', { name: 'Start run' }));
    const alert = await within(dialog).findByRole('alert');
    expect(alert).toHaveTextContent('Target caps exceeded.');
    expect(alert).toHaveTextContent('rate 500/s is above the cap of 5/s');
  });
});
