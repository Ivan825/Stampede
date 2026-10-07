import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import type { RunCreate } from '@/api/types';
import { renderWithRouter } from '@/test/render';
import { installMockApi, server } from '@/test/server';
import { NewRunDialog } from './NewRunDialog';
import { regionsError, toRegions } from './runForm';

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
  renderWithRouter(<NewRunDialog projectId={project.id} open onOpenChange={() => undefined} />);
  return { bodies, user };
}

describe('region split', () => {
  it('checks shares like the server', () => {
    expect(regionsError([])).toBeUndefined();
    expect(
      regionsError([
        { region: 'eu', percent: '60' },
        { region: 'us', percent: '40%' },
      ]),
    ).toBeUndefined();
    expect(regionsError([{ region: 'eu', percent: '60' }])).toBe(
      'The shares add up to 60%, not 100%.',
    );
    expect(regionsError([{ region: 'e u', percent: '100' }])).toMatch(/Region names/);
    expect(
      regionsError([
        { region: 'eu', percent: '0' },
        { region: 'us', percent: '100' },
      ]),
    ).toMatch(/above 0/);
    expect(
      toRegions([
        { region: ' eu ', percent: '60%' },
        { region: 'us', percent: '40' },
      ]),
    ).toEqual({
      eu: 60,
      us: 40,
    });
  });

  it('offers connected regions and sends the split as an override', async () => {
    const { user, bodies } = setup();
    const dialog = await screen.findByRole('dialog', { name: 'New run' });
    await waitFor(() =>
      expect(within(dialog).getByLabelText('Scenario')).toHaveDisplayValue('shop-smoke'),
    );
    expect(await within(dialog).findByText(/Connected regions: .*eu-west-1/)).toBeInTheDocument();

    await user.click(within(dialog).getByRole('button', { name: 'Add region' }));
    await user.type(within(dialog).getByLabelText('Region 1'), 'eu-west-1');
    await user.type(within(dialog).getByLabelText('Region 1 share'), '70');
    await user.click(within(dialog).getByRole('button', { name: 'Add region' }));
    await user.type(within(dialog).getByLabelText('Region 2'), 'us-east-1');
    await user.type(within(dialog).getByLabelText('Region 2 share'), '20');
    await user.click(screen.getByRole('button', { name: 'Start run' }));
    expect(within(dialog).getByText('The shares add up to 90%, not 100%.')).toBeInTheDocument();
    expect(bodies).toHaveLength(0);

    await user.clear(within(dialog).getByLabelText('Region 2 share'));
    await user.type(within(dialog).getByLabelText('Region 2 share'), '30%');
    await user.click(screen.getByRole('button', { name: 'Start run' }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]!.overrides).toEqual({ regions: { 'eu-west-1': 70, 'us-east-1': 30 } });
  });
});
