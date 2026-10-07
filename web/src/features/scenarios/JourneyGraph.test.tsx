import { fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
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

async function select(name: RegExp) {
  const graph = within(screen.getByRole('group', { name: 'Journey graph' }));
  // jsdom cannot measure nodes, so React Flow keeps them hidden and they
  // have no accessible name; find them by their label instead.
  // A plain click: d3-drag's mousedown handler needs a window jsdom's
  // pointer events lack.
  fireEvent.click(await graph.findByLabelText(name));
  return within(screen.getByRole('complementary', { name: 'Selected step' }));
}

describe('JourneyGraphView', () => {
  it('shows a selected request read-only, with no way to edit it', async () => {
    render(<JourneyGraphView yaml={yaml} />);
    expect(screen.getByText(/1 journeys · 3 steps/)).toBeInTheDocument();
    const panel = await select(/GET home/);
    expect(panel.getByText('HTTP request', { selector: '.label-caps' })).toBeInTheDocument();
    expect(panel.getByText('GET')).toBeInTheDocument();
    expect(panel.getByText('/')).toBeInTheDocument();
    expect(panel.queryByRole('textbox')).not.toBeInTheDocument();
    expect(panel.queryByRole('combobox')).not.toBeInTheDocument();
    expect(panel.queryByRole('button', { name: /Apply|Remove|Move|Add/ })).not.toBeInTheDocument();
  });

  it('shows a think step and closes the details', async () => {
    const user = userEvent.setup();
    render(<JourneyGraphView yaml={yaml} />);
    const panel = await select(/think 2s/);
    expect(panel.getByText('Duration')).toBeInTheDocument();
    expect(panel.getByText('2s')).toBeInTheDocument();
    await user.click(panel.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('complementary', { name: 'Selected step' })).not.toBeInTheDocument();
  });

  it('explains YAML it cannot draw', () => {
    render(<JourneyGraphView yaml="metadata: { name: x }" />);
    expect(screen.getByText('No journeys defined yet.')).toBeInTheDocument();
  });
});
