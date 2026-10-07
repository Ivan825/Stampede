import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { renderApp } from '@/test/render';
import { installMockApi } from '@/test/server';

const user = (db: ReturnType<typeof installMockApi>, email: string) =>
  db.users.find((u) => u.email === email)!;

describe('Project settings', () => {
  it('is in the project navigation', async () => {
    const db = installMockApi();
    const store = db.projects[0]!;
    renderApp(`/projects/${store.id}`);
    const link = await screen.findByRole('link', { name: 'Project settings' });
    expect(link).toHaveAttribute('href', `/projects/${store.id}/settings`);
  });

  it('lets admins change the caps and the dry-run gate', async () => {
    const db = installMockApi();
    const store = db.projects[0]!;
    const u = userEvent.setup();
    renderApp(`/projects/${store.id}/settings`);
    const form = within(await screen.findByRole('form', { name: 'Caps and dry run' }));
    expect(form.getByLabelText('Max duration (s)')).toHaveValue('7200');
    const gate = form.getByRole('checkbox', { name: 'Require a passing dry run before load' });
    expect(gate).toBeChecked();

    await u.type(form.getByLabelText('Max rate (/s)'), '0');
    await u.click(form.getByRole('button', { name: 'Save' }));
    expect(form.getByText('A positive number.')).toBeInTheDocument();

    await u.clear(form.getByLabelText('Max rate (/s)'));
    await u.type(form.getByLabelText('Max rate (/s)'), '250');
    await u.click(gate);
    await u.click(form.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Project settings saved.')).toBeInTheDocument();
    expect(db.projectSettings[store.id]).toEqual({
      caps: { maxRate: 250, maxDurationSeconds: 7200 },
      requireDryRun: false,
    });
  });

  it('is read-only for members who are not admins', async () => {
    const db = installMockApi();
    const store = db.projects[0]!;
    db.meId = user(db, 'ana@acme.dev').id;
    renderApp(`/projects/${store.id}/settings`);
    const card = within(await screen.findByRole('region', { name: 'Caps and dry run' }));
    expect(await card.findByText('≤ 2h')).toBeInTheDocument();
    expect(card.getByText('required')).toBeInTheDocument();
    expect(screen.queryByRole('form', { name: 'Caps and dry run' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();

    const members = within(await screen.findByRole('table', { name: 'Project members' }));
    expect(members.queryAllByRole('combobox')).toHaveLength(0);
    const kenji = members.getByText('Kenji Mori').closest('tr')!;
    expect(within(kenji).getByText('Project override')).toBeInTheDocument();
    expect(within(kenji).getByText('editor')).toBeInTheDocument();
  });

  it('lets a project admin through an override change the project', async () => {
    const db = installMockApi();
    const store = db.projects[0]!;
    const kenji = user(db, 'kenji@acme.dev');
    db.meId = kenji.id;
    db.projectRoles[store.id] = [
      { userId: kenji.id, role: 'admin', createdAt: '2026-10-01T00:00:00Z' },
    ];
    const first = renderApp(`/projects/${store.id}/settings`);
    expect(await screen.findByRole('form', { name: 'Caps and dry run' })).toBeInTheDocument();
    expect(screen.getByText(/Your role here:/)).toHaveTextContent('Your role here: admin');
    first.unmount();
    // Not in another project, where Kenji is a runner.
    const pay = db.projects[1]!;
    renderApp(`/projects/${pay.id}/settings`);
    expect(await screen.findByText(/Read-only/)).toBeInTheDocument();
  });

  it('sets and removes per-project role overrides', async () => {
    const db = installMockApi();
    const store = db.projects[0]!;
    const u = userEvent.setup();
    renderApp(`/projects/${store.id}/settings`);
    const members = within(await screen.findByRole('table', { name: 'Project members' }));

    const owner = members.getByText('Priya Shah').closest('tr')!;
    expect(within(owner).getByText('Owners are owners in every project')).toBeInTheDocument();
    expect(within(owner).queryByRole('combobox')).not.toBeInTheDocument();

    const ana = user(db, 'ana@acme.dev');
    const anaRole = members.getByRole('combobox', { name: 'Role for Ana Lima in this project' });
    expect(anaRole).toHaveValue('');
    expect(within(anaRole).queryByRole('option', { name: 'owner' })).not.toBeInTheDocument();
    await u.selectOptions(anaRole, 'viewer');
    expect(await screen.findByText('Ana Lima is now viewer in this project.')).toBeInTheDocument();
    expect(db.projectRoles[store.id]).toContainEqual(
      expect.objectContaining({ userId: ana.id, role: 'viewer' }),
    );

    const kenji = user(db, 'kenji@acme.dev');
    await u.click(
      members.getByRole('button', { name: 'Remove the project override for Kenji Mori' }),
    );
    await waitFor(() =>
      expect(db.projectRoles[store.id]!.some((o) => o.userId === kenji.id)).toBe(false),
    );
    expect(
      await screen.findByText('Kenji Mori has their organisation role (runner) here again.'),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(
        members.getByRole('combobox', { name: 'Role for Kenji Mori in this project' }),
      ).toHaveValue(''),
    );
  });
});
