import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { ReportJourney, Stats } from '@/api/types';
import { metricValue } from '@/lib/format';
import { SlowestRequests, WebVitals } from './ReportView';

const stats = {
  requests: 10,
  failed: 0,
  errorRate: 0,
  rps: 1,
  latency: { count: 10, min: 0, mean: 0, p50: 0, p90: 0, p95: 0, p99: 0, p999: 0, max: 0 },
} as unknown as Stats;

describe('SlowestRequests', () => {
  it('links trace IDs when a trace URL is set', () => {
    const journeys: ReportJourney[] = [
      {
        name: 'checkout',
        stats,
        steps: [
          {
            id: 1,
            name: 'POST /orders',
            stats,
            phases: {},
            slowest: [
              {
                latency: 1.25,
                at: '2026-10-01T10:00:12Z',
                t: 12.3,
                traceId: '4bf92f3577b34da6a3ce929d0e0e4736',
                traceUrl: 'https://jaeger.example.com/trace/4bf92f3577b34da6a3ce929d0e0e4736',
                status: 200,
              },
              { latency: 0.9, at: '2026-10-01T10:00:20Z', t: 20, traceId: 'abc', error: 'timeout' },
            ],
          },
        ],
      },
    ];
    render(<SlowestRequests journeys={journeys} />);
    const link = screen.getByRole('link', { name: /4bf92f3577b34da6a3ce929d0e0e4736/ });
    expect(link).toHaveAttribute(
      'href',
      'https://jaeger.example.com/trace/4bf92f3577b34da6a3ce929d0e0e4736',
    );
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    const rows = screen.getAllByRole('row');
    expect(within(rows[1]!).getByText('t+12.3s')).toBeInTheDocument();
    expect(within(rows[2]!).getByText('timeout')).toBeInTheDocument();
    // Without a template the ID is shown as text.
    expect(within(rows[2]!).queryByRole('link')).not.toBeInTheDocument();
    expect(within(rows[2]!).getByText('abc')).toBeInTheDocument();
  });

  it('renders nothing without slow requests', () => {
    const { container } = render(
      <SlowestRequests
        journeys={[{ name: 'j', stats, steps: [{ id: 1, name: 's', stats, phases: {} }] }]}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});

describe('metricValue', () => {
  it('formats compactly', () => {
    expect(metricValue(0)).toBe('0');
    expect(metricValue(0.5)).toBe('0.5');
    expect(metricValue(12.345)).toBe('12.3');
    expect(metricValue(1234)).toBe('1234');
    expect(metricValue(45600)).toBe('45.6k');
    expect(metricValue(120e6)).toBe('120M');
  });
});

describe('WebVitals', () => {
  it('shows page timings and CLS for browser steps only', () => {
    const journeys: ReportJourney[] = [
      {
        name: 'buy',
        stats,
        steps: [
          {
            id: 1,
            name: 'browser /',
            stats,
            phases: {},
            browser: {
              fcp: { mean: 0.12, p95: 0.2 },
              lcp: { mean: 0.3, p95: 0.5 },
              cls: { mean: 0.05, p95: 0.1 },
              load: { mean: 0.4, p95: 0.6 },
            },
          },
          { id: 2, name: 'GET /api', stats, phases: {} },
        ],
      },
    ];
    render(<WebVitals journeys={journeys} />);
    const rows = screen.getAllByRole('row');
    expect(rows).toHaveLength(2);
    expect(within(rows[1]!).getByText('browser /')).toBeInTheDocument();
    expect(within(rows[1]!).getByText('0.050 / 0.100')).toBeInTheDocument();
  });

  it('renders nothing without browser steps', () => {
    const { container } = render(<WebVitals journeys={[{ name: 'a', stats, steps: [] }]} />);
    expect(container).toBeEmptyDOMElement();
  });
});
