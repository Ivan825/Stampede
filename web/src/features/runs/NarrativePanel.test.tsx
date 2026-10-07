import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { Narrative } from '@/api/types';
import { NarrativePanel } from './NarrativePanel';

const narrative: Narrative = {
  model: 'claude-sonnet-5-5',
  summary: 'The run missed its checkout target.',
  claims: [
    { text: 'p95 latency was 840.0ms.', label: 'measured', refs: ['overall.latency'] },
    { text: 'Connect time suggests pool exhaustion.', label: 'suspected', refs: ['step.buy/pay'] },
  ],
  facts: [
    { id: 'overall.latency', where: 'summary', text: 'latency from scheduled send p95 840.0ms' },
    { id: 'step.buy/pay', where: 'journeys and steps', text: 'buy › pay: mean connect 120.0ms' },
  ],
};

describe('NarrativePanel', () => {
  it('shows each claim with its label and citations', () => {
    render(<NarrativePanel runId="r1" narrative={narrative} />);
    expect(screen.getByText('The run missed its checkout target.')).toBeInTheDocument();
    const claims = screen.getAllByRole('listitem');
    expect(claims).toHaveLength(2);
    expect(within(claims[0]!).getByText('measured')).toBeInTheDocument();
    expect(within(claims[0]!).getByText('overall.latency')).toBeInTheDocument();
    expect(within(claims[1]!).getByText('suspected')).toBeInTheDocument();
    expect(within(claims[1]!).getByText('step.buy/pay')).toBeInTheDocument();
    expect(screen.getByText(/Written by claude-sonnet-5-5/)).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('points to the CLI when there is no summary, without a button to write one', () => {
    render(<NarrativePanel runId="r1" />);
    expect(screen.getByText('stampede narrative r1')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /write/i })).not.toBeInTheDocument();
  });

  it('shows nothing without a summary or a run', () => {
    const { container } = render(<NarrativePanel />);
    expect(container).toBeEmptyDOMElement();
  });
});
