import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { renderApp } from '@/test/render';
import { installMockApi } from '@/test/server';

function setup(role?: 'viewer' | 'editor' | 'admin') {
  const db = installMockApi();
  if (role) db.meId = db.users.find((u) => u.role === role)!.id;
  const project = db.projects[0]!;
  return { db, project, user: userEvent.setup() };
}

describe('AI jobs', () => {
  it('lists jobs with status, stage, tokens and creator, and points to the CLI to start one', async () => {
    const { project } = setup();
    renderApp(`/projects/${project.id}/ai`);
    expect(await screen.findByRole('heading', { name: 'AI jobs' })).toBeInTheDocument();
    const rows = (await screen.findAllByRole('row')).slice(1);
    expect(rows).toHaveLength(3);
    expect(within(rows[0]!).getByText('needs review')).toBeInTheDocument();
    expect(within(rows[0]!).getByText('51,076')).toBeInTheDocument();
    expect(within(rows[0]!).getByText('ana@acme.dev')).toBeInTheDocument();
    expect(within(rows[1]!).getByText(/approved/)).toBeInTheDocument();
    expect(within(rows[2]!).getByText('failed')).toBeInTheDocument();
    expect(within(rows[2]!).getByText(/401 Unauthorized/)).toBeInTheDocument();
    expect(screen.getByText(/184,220/)).toBeInTheDocument();
    expect(
      screen.getByText('stampede ai jobs create --describe "<what users do>" --target <url>'),
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Generate journeys/ })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'AI jobs' })).toBeInTheDocument();
  });

  it('says how to add a provider when none is configured', async () => {
    const { db, project } = setup('editor');
    db.aiProviders = [];
    renderApp(`/projects/${project.id}/ai`);
    expect(await screen.findByText('No AI provider is configured.')).toBeInTheDocument();
    expect(
      screen.getByText('stampede ai providers set <name> --kind <kind> --api-key-env <VAR>'),
    ).toBeInTheDocument();
  });

  it('shows journeys with expandable dry-run traces, the proposal and its diff, read-only', async () => {
    const { db, project, user } = setup();
    const job = db.aiJobs.find((j) => j.status === 'needs_review')!;
    renderApp(`/projects/${project.id}/ai/${job.id}`);
    const checkout = await screen.findByRole('region', { name: 'Journey checkout' });
    expect(within(checkout).getByText('flagged')).toBeInTheDocument();
    expect(within(checkout).getByText('4 attempts')).toBeInTheDocument();
    expect(
      within(checkout).getByText(/shippingAddress is required \(the spec/),
    ).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Journey browse' })).toHaveTextContent('passed');

    // Expand the trace, then the failing step.
    await user.click(within(checkout).getByText('Pass 1'));
    await user.click(within(checkout).getAllByText('POST /api/checkout')[0]!);
    expect(within(checkout).getByText('{"error":"shippingAddress is required"}')).toBeVisible();
    expect(within(checkout).getByText('check failed: status 201 expected, got 422')).toBeVisible();

    expect(screen.getByText('41,206')).toBeInTheDocument();
    expect(screen.getByLabelText('Proposed scenario YAML')).toHaveTextContent(
      'name: shop-generated',
    );
    await user.click(screen.getByRole('tab', { name: 'Diff' }));
    expect(screen.getByLabelText('Diff against the existing scenario')).toHaveTextContent(
      '+ name: shop-generated',
    );

    // Approval is done from the terminal.
    expect(screen.getByText(/did not pass the dry run/)).toBeInTheDocument();
    expect(screen.getByText(`stampede ai jobs approve ${job.id}`)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Approve/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  it('shows where an approved job was saved', async () => {
    const { db, project } = setup();
    const job = db.aiJobs.find((j) => j.approvedAt)!;
    renderApp(`/projects/${project.id}/ai/${job.id}`);
    expect(await screen.findByText(/Approved .*: saved as/)).toBeInTheDocument();
    expect(await screen.findByRole('link', { name: /^shop-smoke v\d+$/ })).toBeInTheDocument();
    expect(screen.queryByText(/stampede ai jobs approve/)).not.toBeInTheDocument();
  });
});
