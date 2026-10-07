import { screen, within } from '@testing-library/react';
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

  it('shows the caps and the dry-run gate read-only, even to admins', async () => {
    const db = installMockApi();
    const store = db.projects[0]!;
    renderApp(`/projects/${store.id}/settings`);
    const card = within(await screen.findByRole('region', { name: 'Caps and dry run' }));
    expect(await card.findByText('≤ 2h')).toBeInTheDocument();
    expect(card.getByText('required')).toBeInTheDocument();
    expect(
      card.getByText(
        `stampede projects settings set --project ${store.slug} --max-rate <n> --require-dry-run`,
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole('form')).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
  });

  it('shows each member’s role here, with overrides, and no way to change them', async () => {
    const db = installMockApi();
    const store = db.projects[0]!;
    db.meId = user(db, 'ana@acme.dev').id;
    renderApp(`/projects/${store.id}/settings`);
    const members = within(await screen.findByRole('table', { name: 'Project members' }));
    expect(members.queryAllByRole('combobox')).toHaveLength(0);
    expect(members.queryAllByRole('button')).toHaveLength(0);
    const owner = members.getByText('Priya Shah').closest('tr')!;
    expect(within(owner).getByText('Owners are owners in every project')).toBeInTheDocument();
    const kenji = members.getByText('Kenji Mori').closest('tr')!;
    expect(within(kenji).getByText('Project override')).toBeInTheDocument();
    expect(within(kenji).getByText('editor')).toBeInTheDocument();
    expect(
      screen.getByText(`stampede projects roles set <email> <role> --project ${store.slug}`),
    ).toBeInTheDocument();
  });
});
