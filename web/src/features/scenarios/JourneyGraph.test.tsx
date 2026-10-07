import { fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { parse } from 'yaml';
import { JourneyGraphView } from './JourneyGraph';

const yaml = `metadata:
  name: shop
journeys:
  - name: browse
    steps:
      # Landing page.
      - name: home
        get: /
      - think: 2s
      - get: /api/products
load: { vus: 1, duration: 10s }
`;

type Doc = { journeys: { steps: Record<string, unknown>[] }[] };
const steps = (y: string) => (parse(y) as Doc).journeys[0]!.steps;

/** The graph with its YAML held in state, as the scenario editor does. */
function Harness({ onChange }: { onChange: (y: string) => void }) {
  const [y, setY] = useState(yaml);
  return (
    <>
      <JourneyGraphView
        yaml={y}
        onChange={(next) => {
          setY(next);
          onChange(next);
        }}
      />
      <pre data-testid="yaml">{y}</pre>
    </>
  );
}

async function select(name: RegExp) {
  const user = userEvent.setup();
  const graph = within(screen.getByRole('group', { name: 'Journey graph' }));
  // jsdom cannot measure nodes, so React Flow keeps them hidden and they
  // have no accessible name; find them by their label instead.
  // A plain click: d3-drag's mousedown handler needs a window jsdom's
  // pointer events lack.
  fireEvent.click(await graph.findByLabelText(name));
  return { user, panel: within(screen.getByRole('complementary', { name: 'Selected step' })) };
}

describe('JourneyGraphView editing', () => {
  it('edits a request in the side panel and keeps the YAML in sync', async () => {
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    const { user, panel } = await select(/GET home/);
    expect(panel.getByText('HTTP request', { selector: '.label-caps' })).toBeInTheDocument();
    await user.selectOptions(panel.getByLabelText('Method'), 'POST');
    await user.clear(panel.getByLabelText('URL'));
    await user.type(panel.getByLabelText('URL'), '/api/login');
    await user.clear(panel.getByLabelText('Name'));
    await user.type(panel.getByLabelText('Name'), 'sign in');
    await user.click(panel.getByRole('button', { name: 'Apply' }));
    expect(onChange).toHaveBeenCalledTimes(1);
    const out = screen.getByTestId('yaml').textContent;
    expect(steps(out)[0]).toEqual({ name: 'sign in', post: '/api/login' });
    expect(out).toContain('# Landing page.');
    // The graph follows the YAML.
    expect(
      await within(screen.getByRole('group', { name: 'Journey graph' })).findByLabelText(
        /POST sign in/,
      ),
    ).toBeInTheDocument();
  });

  it('reorders, adds and removes steps', async () => {
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    const { user, panel } = await select(/GET home/);
    expect(panel.getByRole('button', { name: /Move up/ })).toBeDisabled();
    await user.click(panel.getByRole('button', { name: /Move down/ }));
    let out = screen.getByTestId('yaml').textContent;
    expect(steps(out).map((s) => s.think ?? s.name)).toEqual(['2s', 'home', undefined]);

    // The moved step stays selected; add a think step after it.
    const after = within(screen.getByRole('complementary', { name: 'Selected step' })).getByRole(
      'group',
      { name: 'Add a step after' },
    );
    await user.click(within(after).getByRole('button', { name: /Think/ }));
    out = screen.getByTestId('yaml').textContent!;
    expect(steps(out)).toHaveLength(4);
    expect(steps(out)[2]).toEqual({ think: '1s' });

    const p2 = within(screen.getByRole('complementary', { name: 'Selected step' }));
    await user.clear(p2.getByLabelText('Duration'));
    await user.type(p2.getByLabelText('Duration'), '1s..3s');
    await user.click(p2.getByRole('button', { name: 'Apply' }));
    expect(steps(screen.getByTestId('yaml').textContent)[2]).toEqual({ think: '1s..3s' });

    // Each edit renders the panel afresh from the new YAML.
    const p3 = within(screen.getByRole('complementary', { name: 'Selected step' }));
    expect(p3.getByLabelText('Duration')).toHaveValue('1s..3s');
    await user.click(p3.getByRole('button', { name: /Remove step/ }));
    out = screen.getByTestId('yaml').textContent!;
    expect(steps(out)).toHaveLength(3);
    expect(screen.queryByRole('complementary', { name: 'Selected step' })).not.toBeInTheDocument();
  });

  it('adds a group at the end of a journey', async () => {
    render(<Harness onChange={() => undefined} />);
    const { user, panel } = await select(/journey browse/);
    const add = within(panel.getByRole('group', { name: 'Add a step at the end' }));
    await user.click(add.getByRole('button', { name: /Group/ }));
    const out = screen.getByTestId('yaml').textContent;
    expect(steps(out)[3]).toEqual({
      group: 'new group',
      steps: [{ name: 'new request', get: '/' }],
    });
  });

  it('shows details without editing when read only', async () => {
    render(<JourneyGraphView yaml={yaml} readOnly onChange={() => undefined} />);
    const { panel } = await select(/GET home/);
    expect(panel.getByText(/Read only/)).toBeInTheDocument();
    expect(panel.queryByRole('button', { name: 'Apply' })).not.toBeInTheDocument();
    expect(panel.queryByRole('button', { name: /Remove step/ })).not.toBeInTheDocument();
  });
});
