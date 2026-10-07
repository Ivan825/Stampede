import { describe, expect, it } from 'vitest';
import { checkoutStress } from '@/mocks/yamls';
import { buildJourneyGraph, nodeKey, stepPath } from './graphLayout';

describe('buildJourneyGraph', () => {
  it('fans out from the start node to each journey by weight', () => {
    const r = buildJourneyGraph(checkoutStress);
    if (!r.ok) throw new Error(r.error);
    const { nodes, edges } = r.graph;
    const start = nodes.find((n) => n.kind === 'start')!;
    expect(start.title).toBe('checkout-stress');
    const toJourneys = edges.filter((e) => e.source === start.id);
    expect(toJourneys.map((e) => e.label)).toEqual(['70%', '30%']);
    expect(r.graph.journeys).toBe(2);
  });

  it('draws branches as columns, loops with a back edge and think steps as small nodes', () => {
    const r = buildJourneyGraph(checkoutStress);
    if (!r.ok) throw new Error(r.error);
    const { nodes, edges } = r.graph;
    const branch = nodes.find((n) => n.kind === 'branch')!;
    const arms = edges.filter((e) => e.source === branch.id);
    expect(arms.map((e) => e.label)).toEqual(['buy · 30%', 'browse more · 70%']);
    const armX = arms.map((e) => nodes.find((n) => n.id === e.target)!.x);
    expect(armX[1]).toBeGreaterThan(armX[0]!);

    const loop = nodes.find((n) => n.kind === 'loop')!;
    expect(loop.title).toBe('loop × 3');
    expect(edges.some((e) => e.back && e.target === loop.id)).toBe(true);
    expect(nodes.find((n) => n.kind === 'group')!.title).toBe('group checkout');
    expect(nodes.filter((n) => n.kind === 'think').map((n) => n.title)).toContain('think 1s..2s');

    const pay = nodes.find((n) => n.title === 'pay')!;
    expect(pay.method).toBe('POST');
    expect(pay.subtitle).toBe('/api/checkout/pay');
  });

  it('gives every step its place in the YAML', () => {
    const r = buildJourneyGraph(checkoutStress);
    if (!r.ok) throw new Error(r.error);
    const byTitle = (t: string) => r.graph.nodes.find((n) => n.title === t)!;
    expect(stepPath(byTitle('home'))).toEqual(['journeys', 0, 'steps', 0]);
    expect(stepPath(byTitle('pay'))).toEqual([
      'journeys',
      0,
      'steps',
      3,
      'branch',
      0,
      'steps',
      2,
      'steps',
      1,
    ]);
    const group = r.graph.nodes.find((n) => n.kind === 'group')!;
    expect(group.body).toEqual(['journeys', 0, 'steps', 3, 'branch', 0, 'steps', 2, 'steps']);
    expect(group.fields).toEqual({ group: 'checkout' });
    const journey = r.graph.nodes.find((n) => n.kind === 'journey')!;
    expect(nodeKey(journey)).toBe('journey:journeys/0/steps');
    expect(byTitle('search').fields).toEqual({
      name: 'search',
      url: '/api/search?q=${pick(["boots", "socks", "jacket"])}',
    });
  });

  it('reports YAML errors and missing journeys', () => {
    const bad = buildJourneyGraph('journeys: [');
    expect(bad.ok).toBe(false);
    const empty = buildJourneyGraph('metadata: { name: x }');
    expect(empty).toEqual({ ok: false, error: 'No journeys defined yet.' });
  });
});
