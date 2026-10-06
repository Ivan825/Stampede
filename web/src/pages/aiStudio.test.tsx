import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { AIJobApprove, AIJobCreate } from '@/api/types';
import { renderApp } from '@/test/render';
import { installMockApi, server } from '@/test/server';

// Monaco needs a real browser; the proposal is shown as plain text instead.
vi.mock('@/features/scenarios/YamlEditor', () => ({
  YamlEditor: ({ value, readOnly }: { value: string; readOnly?: boolean }) => (
    <pre data-testid="yaml-editor" data-readonly={readOnly ? 'true' : 'false'}>
      {value}
    </pre>
  ),
}));

function setup(role?: 'viewer' | 'editor' | 'admin') {
  const db = installMockApi();
  if (role) db.meId = db.users.find((u) => u.role === role)!.id;
  const project = db.projects[0]!;
  return { db, project, user: userEvent.setup() };
}

/** Records the JSON bodies of requests to a path. */
function capture<T>(method: string, pathEnd: string): T[] {
  const bodies: T[] = [];
  server.events.on('request:start', ({ request }) => {
    if (request.method === method && new URL(request.url).pathname.endsWith(pathEnd))
      void request
        .clone()
        .json()
        .then((b) => bodies.push(b as T));
  });
  return bodies;
}

describe('AI studio', () => {
  it('lists jobs with status, stage, tokens and creator', async () => {
    const { project } = setup();
    renderApp(`/projects/${project.id}/ai`);
    expect(await screen.findByRole('heading', { name: 'AI studio' })).toBeInTheDocument();
    const rows = (await screen.findAllByRole('row')).slice(1);
    expect(rows).toHaveLength(3);
    expect(within(rows[0]!).getByText('needs review')).toBeInTheDocument();
    expect(within(rows[0]!).getByText('51,076')).toBeInTheDocument();
    expect(within(rows[0]!).getByText('ana@acme.dev')).toBeInTheDocument();
    expect(within(rows[1]!).getByText(/approved/)).toBeInTheDocument();
    expect(within(rows[2]!).getByText('failed')).toBeInTheDocument();
    expect(within(rows[2]!).getByText(/401 Unauthorized/)).toBeInTheDocument();
    expect(screen.getByText(/184,220/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'AI studio' })).toBeInTheDocument();
  });

  it('explains that an admin adds a provider when none is configured', async () => {
    const { db, project } = setup('editor');
    db.aiProviders = [];
    renderApp(`/projects/${project.id}/ai`);
    expect(await screen.findByText('No AI provider is configured.')).toBeInTheDocument();
    expect(screen.getByText(/An admin adds one in Settings → AI providers/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Generate journeys/ })).toBeDisabled();
  });

  it('links admins to the AI providers settings when none is configured', async () => {
    const { db, project, user } = setup();
    db.aiProviders = [];
    const { router } = renderApp(`/projects/${project.id}/ai`);
    await user.click(await screen.findByRole('link', { name: 'Settings → AI providers' }));
    await waitFor(() => expect(router.state.location.pathname).toBe('/settings'));
    expect(await screen.findByRole('tab', { name: 'AI providers' })).toHaveAttribute(
      'data-state',
      'active',
    );
  });

  it('is read-only for viewers', async () => {
    const { db, project } = setup('viewer');
    renderApp(`/projects/${project.id}/ai`);
    expect(await screen.findByText('needs review')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Generate journeys/ })).not.toBeInTheDocument();

    const job = db.aiJobs.find((j) => j.status === 'needs_review')!;
    renderApp(`/projects/${project.id}/ai/${job.id}`);
    expect(await screen.findByText('Editors can approve this proposal.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Approve/ })).not.toBeInTheDocument();
    expect(screen.getByTestId('yaml-editor')).toHaveAttribute('data-readonly', 'true');
  });

  it('generates journeys from a description and a pasted spec, then shows live progress', async () => {
    const { db, project, user } = setup('editor');
    const bodies = capture<AIJobCreate>('POST', `/projects/${project.id}/ai/jobs`);
    const { router } = renderApp(`/projects/${project.id}/ai`);
    await user.click(await screen.findByRole('button', { name: /Generate journeys/ }));
    const dialog = await screen.findByRole('dialog', { name: 'Generate journeys' });

    // Nothing given: refused before it is sent, and the dry run needs a target.
    await user.click(within(dialog).getByRole('button', { name: 'Generate' }));
    expect(within(dialog).getByText(/Give at least one input/)).toBeInTheDocument();
    expect(within(dialog).getByText('Pick the target to dry-run against.')).toBeInTheDocument();

    await user.type(within(dialog).getByLabelText('Description'), 'Shoppers browse and buy.');
    const spec = within(dialog).getByRole('group', { name: 'OpenAPI spec input' });
    await user.click(within(spec).getByRole('button', { name: 'Paste' }));
    await user.click(within(dialog).getByLabelText('OpenAPI spec text'));
    await user.paste('openapi: 3.0.3\npaths: {}');
    const staging = db.targets.find((t) => t.name === 'staging')!;
    await user.selectOptions(within(dialog).getByLabelText('Target'), staging.id);
    await user.selectOptions(within(dialog).getByLabelText('Repair rounds'), '1');
    await user.click(within(dialog).getByRole('button', { name: 'Generate' }));

    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toEqual({
      dryRun: true,
      maxRepairs: 1,
      description: 'Shoppers browse and buy.',
      openapi: 'openapi: 3.0.3\npaths: {}',
      targetId: staging.id,
    });
    await waitFor(() => expect(router.state.location.pathname).toMatch(/\/ai\/[0-9a-f-]{36}$/));
    expect(await screen.findByRole('status', { name: 'Progress' })).toBeInTheDocument();
    expect(screen.getByText(/The proposal appears here when the job finishes/)).toBeInTheDocument();
  });

  it('refuses a file above the server limit without reading it', async () => {
    const { project, user } = setup('editor');
    renderApp(`/projects/${project.id}/ai`);
    await user.click(await screen.findByRole('button', { name: /Generate journeys/ }));
    const dialog = await screen.findByRole('dialog', { name: 'Generate journeys' });
    const big = new File(['{}'], 'spec.json', { type: 'application/json' });
    Object.defineProperty(big, 'size', { value: 6 * 1024 * 1024 });
    await user.upload(within(dialog).getByLabelText('OpenAPI spec file'), big);
    expect(
      await within(dialog).findByText(
        'spec.json is 6.00 MiB; the server accepts at most 5.00 MiB.',
      ),
    ).toBeInTheDocument();

    const har = new File(['{"log":{"entries":[]}}'], 'session.har');
    await user.upload(within(dialog).getByLabelText('HAR recording file'), har);
    expect(await within(dialog).findByText('session.har')).toBeInTheDocument();
  });

  it('shows journeys with expandable dry-run traces, the diff and token use', async () => {
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
    expect(screen.getByTestId('yaml-editor')).toHaveTextContent('name: shop-generated');
    await user.click(screen.getByRole('tab', { name: 'Diff' }));
    expect(screen.getByLabelText('Diff against the existing scenario')).toHaveTextContent(
      '+ name: shop-generated',
    );
  });

  it('approves a job that needs review only after confirming, as a new version', async () => {
    const { db, project, user } = setup('editor');
    const job = db.aiJobs.find((j) => j.status === 'needs_review')!;
    const smoke = db.scenarios.find((s) => s.id === job.scenarioId)!;
    const before = smoke.latestVersion.version;
    const bodies = capture<AIJobApprove>('POST', `/ai/jobs/${job.id}/approve`);
    renderApp(`/projects/${project.id}/ai/${job.id}`);

    expect(await screen.findByText(/did not pass the dry run/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByLabelText('Save as')).toHaveValue(smoke.id));
    await user.type(screen.getByLabelText('Message'), 'Generated checkout journey');
    await user.click(screen.getByRole('button', { name: 'Approve with flagged journeys…' }));
    const confirm = await screen.findByRole('alertdialog');
    expect(within(confirm).getByText('checkout')).toBeInTheDocument();
    await user.click(within(confirm).getByRole('button', { name: 'Approve anyway' }));

    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toEqual({
      scenarioId: smoke.id,
      message: 'Generated checkout journey',
      allowUnvalidated: true,
    });
    const link = await screen.findByRole('link', { name: `shop-smoke v${before + 1}` });
    expect(link).toHaveAttribute(
      'href',
      `/projects/${project.id}/scenarios/${smoke.id}?version=${before + 1}`,
    );
    expect(db.versions[smoke.id]![0]!.message).toBe('Generated checkout journey');
    expect(screen.queryByRole('button', { name: /Approve/ })).not.toBeInTheDocument();
  });

  it('approves a validated job as a new scenario without a confirmation', async () => {
    const { db, project, user } = setup('editor');
    const job = db.aiJobs.find((j) => j.status === 'needs_review')!;
    Object.assign(job, { status: 'succeeded', scenarioId: null });
    job.journeys = job.journeys.map((j) => ({ ...j, status: 'passed', problems: [] }));
    renderApp(`/projects/${project.id}/ai/${job.id}`);
    expect(await screen.findByLabelText('Save as')).toHaveValue('');
    await user.click(screen.getByRole('button', { name: 'Approve and save' }));
    expect(await screen.findByRole('link', { name: 'shop-generated v1' })).toBeInTheDocument();
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(db.scenarios.some((s) => s.name === 'shop-generated')).toBe(true);
  });

  it('shows where an approved job was saved', async () => {
    const { db, project } = setup();
    const job = db.aiJobs.find((j) => j.approvedAt)!;
    renderApp(`/projects/${project.id}/ai/${job.id}`);
    expect(await screen.findByText(/Approved .*: saved as/)).toBeInTheDocument();
    expect(await screen.findByRole('link', { name: /^shop-smoke v\d+$/ })).toBeInTheDocument();
  });
});

describe('AI providers settings', () => {
  it('adds a provider without ever showing its key, and replaces one keeping the key', async () => {
    const { db, user } = setup('admin');
    renderApp('/settings?tab=ai');
    const row = (await screen.findByText('default')).closest('tr')!;
    expect(within(row).getByText('stored')).toBeInTheDocument();
    expect(within(row).getByText('claude-sonnet-5-5')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /Add provider/ }));
    const dialog = await screen.findByRole('dialog', { name: 'Add AI provider' });
    const name = within(dialog).getByLabelText('Name');
    await user.clear(name);
    await user.type(name, 'local');
    await user.selectOptions(within(dialog).getByLabelText('Kind'), 'openai-compatible');
    await user.click(within(dialog).getByRole('button', { name: 'Add provider' }));
    expect(within(dialog).getByText(/Enter the model name/)).toBeInTheDocument();
    expect(within(dialog).getByText(/Enter the server URL/)).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText('Model'), 'qwen3:14b');
    await user.type(within(dialog).getByLabelText('Base URL'), 'http://llm.internal:8000/v1');
    await user.type(within(dialog).getByLabelText('API key (optional)'), 'sk-local-123');
    await user.click(within(dialog).getByRole('button', { name: 'Add provider' }));

    const local = (await screen.findByText('local')).closest('tr')!;
    expect(within(local).getByText('http://llm.internal:8000/v1')).toBeInTheDocument();
    expect(screen.queryByText('sk-local-123')).not.toBeInTheDocument();
    expect(db.aiProviders.find((p) => p.name === 'local')?.hasKey).toBe(true);

    await user.click(within(row).getByRole('button', { name: 'Replace default' }));
    const edit = await screen.findByRole('dialog', { name: 'Replace default' });
    expect(
      within(edit).getByText('A key is stored. Leave this empty to keep it.'),
    ).toBeInTheDocument();
    const cap = within(edit).getByLabelText('Monthly token cap');
    await user.clear(cap);
    await user.type(cap, '500000');
    await user.click(within(edit).getByRole('button', { name: 'Save provider' }));
    await waitFor(() =>
      expect(db.aiProviders.find((p) => p.name === 'default')?.monthlyTokenCap).toBe(500000),
    );
    expect(db.aiProviders.find((p) => p.name === 'default')?.hasKey).toBe(true);
  });

  it('is hidden from editors', async () => {
    setup('editor');
    renderApp('/settings?tab=ai');
    expect(await screen.findByRole('tab', { name: 'Account' })).toHaveAttribute(
      'data-state',
      'active',
    );
    expect(screen.queryByRole('tab', { name: 'AI providers' })).not.toBeInTheDocument();
  });
});
