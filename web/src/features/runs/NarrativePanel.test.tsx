import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it } from 'vitest';
import type { Narrative } from '@/api/types';
import { NarrativePanel } from './NarrativePanel';

function wrap(children: ReactNode) {
  return <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;
}

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
    render(wrap(<NarrativePanel runId="r1" narrative={narrative} canWrite={false} />));
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

  it('offers to write a summary only to those allowed', () => {
    const { rerender } = render(wrap(<NarrativePanel runId="r1" canWrite />));
    expect(screen.getByRole('button', { name: /write a summary/i })).toBeInTheDocument();
    rerender(wrap(<NarrativePanel runId="r1" canWrite={false} />));
    expect(screen.queryByText('AI summary')).not.toBeInTheDocument();
  });
});
