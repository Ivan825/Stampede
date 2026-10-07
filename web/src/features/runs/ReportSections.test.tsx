import { QueryClientProvider } from '@tanstack/react-query';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { testQueryClient } from '@/test/render';
import type { CurvePoint, ErrorRow, Report, WorkerRow } from '@/api/types';
import { analyse, buildReport, simulateTimeline } from '@/mocks/sim';
import { catalogBreakpoint, checkoutStress } from '@/mocks/yamls';
import { BandLegend } from './ReportCharts';
import { markAreaOf, requestText, responseText, timeBandsOf } from './reportData';
import {
  BreakpointSection,
  CurveSection,
  ErrorsSection,
  RecoverySection,
  WorkersSection,
} from './ReportSections';
import { ReportView } from './ReportView';

// ECharts needs a canvas, which jsdom lacks; the charts are covered by
// their pure helpers below.
vi.mock('./ReportCharts', async (orig) => {
  const real = await orig<typeof import('./ReportCharts')>();
  return {
    ...real,
    CurveChart: () => <div data-testid="curve-chart" />,
    ReportCharts: ({ bands }: { bands?: unknown[] }) => (
      <div data-testid="timeline-charts" data-bands={bands?.length ?? 0} />
    ),
    TargetMetricCharts: () => <div data-testid="metric-charts" />,
  };
});

const point = (offered: number, throughput: number, p95: number): CurvePoint => ({
  offered,
  throughput,
  rps: throughput * 2,
  p50: p95 / 2,
  p95,
  p99: p95 * 1.5,
  errorRate: 0,
  seconds: 30,
});

describe('BreakpointSection', () => {
  it('lists the confirmation holds after the first failing step', () => {
    render(
      <BreakpointSection
        bp={{
          found: true,
          lastPass: 300,
          firstFail: 400,
          unit: '/s',
          failedOn: ['http.p95 < 250ms'],
          refined: [
            { level: 350, pass: true },
            { level: 375, pass: false, failedOn: ['errors < 1%'] },
          ],
        }}
      />,
    );
    expect(screen.getByText('300/s')).toBeInTheDocument();
    expect(screen.getByText('400/s')).toBeInTheDocument();
    expect(screen.getByText(/Narrowed by 2 confirmation holds/)).toBeInTheDocument();
    const holds = within(screen.getByRole('list', { name: 'Confirmation holds' }));
    expect(holds.getByText('350/s held')).toBeInTheDocument();
    const failed = holds.getByText('375/s failed');
    expect(failed).toHaveAttribute('title', 'failed on errors < 1%');
  });

  it('says when the breakpoint was not reached', () => {
    render(<BreakpointSection bp={{ found: false, lastPass: 1000, unit: '/s' }} />);
    expect(screen.getByText(/Not reached: every target held/)).toBeInTheDocument();
    expect(screen.queryByRole('list', { name: 'Confirmation holds' })).not.toBeInTheDocument();
  });
});

describe('CurveSection', () => {
  const curve = [point(100, 50, 0.02), point(200, 100, 0.025), point(300, 110, 0.2)];

  it('describes the knee and marks its level in the table', () => {
    render(
      <CurveSection
        curve={curve}
        unit="/s"
        knee={{
          found: true,
          at: curve[1]!,
          next: curve[2]!,
          unit: '/s',
          reason: 'throughput stopped growing with load',
        }}
      />,
    );
    expect(screen.getByText(/Throughput kept up with load to/)).toHaveTextContent(
      'Throughput kept up with load to 200/s. At 300/s throughput stopped growing with load: 110 it/s, p95 200.0ms.',
    );
    expect(screen.getByTestId('curve-chart')).toBeInTheDocument();
    const rows = screen.getAllByRole('row');
    expect(rows).toHaveLength(4);
    expect(within(rows[3]!).getByText('knee')).toBeInTheDocument();
  });

  it('says there is no knee when throughput kept up', () => {
    render(
      <CurveSection
        curve={curve}
        unit=" VUs"
        knee={{ found: false, at: curve[2]!, unit: ' VUs' }}
      />,
    );
    expect(screen.getByText(/No knee/)).toHaveTextContent(
      'No knee: throughput kept up with load at every level up to 300 VUs.',
    );
  });

  it('renders nothing for a single level', () => {
    const { container } = render(<CurveSection curve={[curve[0]!]} unit="/s" />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe('RecoverySection', () => {
  it('shows how long the target took to recover', () => {
    render(
      <RecoverySection
        recovery={{
          recovered: true,
          seconds: 12,
          normalAt: 90,
          baselineP95: 0.042,
          baselineErrorRate: 0.001,
        }}
      />,
    );
    expect(screen.getByText(/Back to normal/)).toHaveTextContent(
      'Back to normal 12s after load returned to normal at 90s: p95 within 25% of the baseline 42.0ms and errors within a point of 0.10%, for three seconds in a row.',
    );
  });

  it('says when the target did not recover', () => {
    render(
      <RecoverySection
        recovery={{ recovered: false, normalAt: 90, baselineP95: 0.042, baselineErrorRate: 0 }}
      />,
    );
    expect(screen.getByText('Not back to normal')).toBeInTheDocument();
  });
});

describe('WorkersSection', () => {
  const workers: WorkerRow[] = [
    {
      id: 'a',
      name: 'gen-1',
      region: 'eu-west-1',
      shareLo: 0,
      shareHi: 0.5,
      state: 'finished',
      peakVUs: 40,
      requests: 12000,
      saturated: [
        { from: 30, to: 36 },
        { from: 50, to: 52 },
      ],
      saturationReasons: ['cpu', 'sched-lag'],
      clockOffset: 0.0012,
    },
    {
      id: 'b',
      name: 'gen-2',
      shareLo: 0.5,
      shareHi: 1,
      state: 'lost',
      peakVUs: 40,
      requests: 6000,
      lost: { from: 40, to: 60 },
      clockOffset: -0.0004,
    },
    {
      id: 'c',
      name: 'gen-3',
      shareLo: 0.5,
      shareHi: 1,
      state: 'finished',
      replaces: 'gen-2',
      peakVUs: 40,
      requests: 3000,
      clockOffset: 0,
    },
  ];

  it('lists share, state, saturated and lost windows and clock offset', () => {
    render(<WorkersSection workers={workers} />);
    const rows = screen.getAllByRole('row');
    expect(rows).toHaveLength(4);
    const one = within(rows[1]!);
    expect(one.getByText('gen-1')).toBeInTheDocument();
    expect(one.getByText('eu-west-1')).toBeInTheDocument();
    expect(one.getByText('50.0%')).toBeInTheDocument();
    expect(one.getByText('30s–36s, 50s–52s')).toBeInTheDocument();
    expect(one.getByText('(cpu, sched-lag)')).toBeInTheDocument();
    expect(one.getByText('1.2ms')).toBeInTheDocument();
    const two = within(rows[2]!);
    expect(two.getByText('lost')).toHaveClass('text-fail');
    expect(two.getByText('40s–60s')).toBeInTheDocument();
    expect(two.getByText('-0.4ms')).toBeInTheDocument();
    expect(within(rows[3]!).getByText('replaces gen-2')).toBeInTheDocument();
  });

  it('renders nothing without workers', () => {
    const { container } = render(<WorkersSection workers={[]} />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe('ErrorsSection', () => {
  const errors: ErrorRow[] = [
    {
      journey: 'shopper',
      step: 'pay',
      error: 'status 503',
      count: 12,
      examples: [
        {
          at: '2026-10-01T10:00:12Z',
          traceId: '4bf92f3577b34da6a3ce929d0e0e4736',
          request: 'POST http://shop.local/api/checkout/pay',
          requestHeaders: { 'Content-Type': 'application/json', Authorization: '[redacted]' },
          requestBody: '{"card":"[redacted]"}',
          status: 503,
          responseHeaders: { 'Retry-After': '1' },
          responseBody: '{"error":"overloaded"}',
          detail: 'status 503',
        },
        {
          at: '2026-10-01T10:00:20Z',
          request: 'POST http://shop.local/api/checkout/pay',
          detail: 'connection refused',
        },
      ],
    },
    { journey: 'shopper', step: 'home', error: 'timeout after 30s', count: 2 },
  ];

  it('expands a row to the request and response of each example', async () => {
    const user = userEvent.setup();
    render(<ErrorsSection errors={errors} />);
    const toggle = screen.getByRole('button', { name: 'Show 2 examples' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByText('none')).toBeInTheDocument();
    await user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(toggle).toHaveAccessibleName('Hide 2 examples');

    const req = screen.getByRole('region', { name: 'Example 1 request' });
    expect(req.querySelector('pre')!.textContent).toBe(
      'POST http://shop.local/api/checkout/pay\nAuthorization: [redacted]\nContent-Type: application/json\n\n{"card":"[redacted]"}',
    );
    const res = screen.getByRole('region', { name: 'Example 1 response' });
    expect(res.querySelector('pre')!.textContent).toBe(
      '503 · status 503\nRetry-After: 1\n\n{"error":"overloaded"}',
    );
    expect(screen.getByText('trace 4bf92f3577b34da6a3ce929d0e0e4736')).toBeInTheDocument();
    const res2 = screen.getByRole('region', { name: 'Example 2 response' });
    expect(res2.querySelector('pre')!.textContent).toBe('no response · connection refused');

    await user.click(toggle);
    expect(screen.queryByRole('region', { name: 'Example 1 request' })).not.toBeInTheDocument();
  });

  it('formats examples like the HTML report', () => {
    const ex = errors[0]!.examples![1]!;
    expect(requestText(ex)).toBe('POST http://shop.local/api/checkout/pay');
    expect(responseText(ex)).toBe('no response · connection refused');
  });
});

describe('timeline bands', () => {
  const report = {
    faults: [
      { label: 'latency 200ms', kind: 'proxy' as const, target: 'db', start: 5, end: 10 },
      {
        label: 'failed',
        kind: 'proxy' as const,
        target: 'db',
        start: 1,
        end: 2,
        error: 'no agent',
      },
    ],
    workers: [
      {
        id: 'a',
        name: 'w1',
        shareLo: 0,
        shareHi: 1,
        state: 'finished',
        peakVUs: 1,
        requests: 1,
        clockOffset: 0,
        saturated: [{ from: 3, to: 6 }],
        saturationReasons: ['cpu'],
        lost: { from: 8, to: 20 },
      },
    ],
  };

  it('collects faults, saturated and lost windows like the HTML report', () => {
    expect(timeBandsOf(report)).toEqual([
      { from: 5, to: 10, label: 'latency 200ms', kind: 'fault' },
      { from: 3, to: 6, label: 'w1 saturated (cpu)', kind: 'saturated' },
      { from: 8, to: 20, label: 'w1 lost', kind: 'lost' },
    ]);
  });

  it('clips mark areas to the chart and drops empty ones', () => {
    const areas = markAreaOf(timeBandsOf(report), 4, 12);
    expect(areas.map(([a, b]) => [a.xAxis, b.xAxis, a.name])).toEqual([
      [5, 10, 'latency 200ms'],
      [4, 6, 'w1 saturated (cpu)'],
      [8, 12, 'w1 lost'],
    ]);
    expect(markAreaOf(timeBandsOf(report), 30, 40)).toEqual([]);
  });

  it('labels each kind of band once', () => {
    render(<BandLegend bands={timeBandsOf(report)} />);
    const items = within(screen.getByRole('list', { name: 'Shaded windows' })).getAllByRole(
      'listitem',
    );
    expect(items.map((i) => i.textContent)).toEqual([
      'injected fault',
      'worker saturated',
      'worker lost',
    ]);
  });
});

function renderWithClient(ui: ReactNode) {
  return render(<QueryClientProvider client={testQueryClient()}>{ui}</QueryClientProvider>);
}

function reportFor(yaml: string, workers: number): Report {
  const scenario = analyse(yaml);
  const plan = scenario.validation.plan!;
  const timeline = simulateTimeline({
    seed: 7,
    mode: plan.mode,
    shape: plan.shape,
    peak: plan.peak,
    duration: Math.min(plan.durationSeconds, 300),
    capacity: 430,
    baseLatency: 0.05,
    baseErrors: 0.002,
  });
  return buildReport({
    runId: 'r1',
    scenario,
    targetURL: 'http://shop.local',
    targets: ['http.p95 < 250ms', 'errors < 1%'],
    timeline,
    started: '2026-10-01T10:00:00Z',
    stopReason: 'completed',
    workers,
    breakpoint: plan.shape === 'breakpoint',
  });
}

describe('ReportView', () => {
  it('shows the curve, workers, refined breakpoint and error examples', () => {
    const report = reportFor(catalogBreakpoint, 2);
    expect(report.curve?.length).toBeGreaterThan(1);
    expect(report.breakpoint?.refined?.length).toBe(2);
    renderWithClient(<ReportView report={report} />);
    const headings = screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent);
    expect(headings).toEqual(
      expect.arrayContaining([
        'Breakpoint',
        'Throughput against load',
        'Over time',
        'Workers',
        'Errors',
      ]),
    );
    expect(screen.getByRole('list', { name: 'Confirmation holds' })).toBeInTheDocument();
    // gen-2 saturates, so the charts get a band.
    expect(screen.getByTestId('timeline-charts')).toHaveAttribute('data-bands', '1');
    expect(screen.getAllByRole('button', { name: /Show \d examples?/ }).length).toBeGreaterThan(0);
  });

  it('shows recovery for a spike and leaves out sections without data', () => {
    const spike = checkoutStress.replace('shape: stress', 'shape: spike');
    const report = reportFor(spike, 1);
    expect(report.recovery).toBeDefined();
    renderWithClient(<ReportView report={report} />);
    expect(screen.getByRole('heading', { name: 'Recovery' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Workers' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Breakpoint' })).not.toBeInTheDocument();
    expect(screen.getByTestId('timeline-charts')).toHaveAttribute('data-bands', '0');
  });
});
