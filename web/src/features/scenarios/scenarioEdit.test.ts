import { describe, expect, it } from 'vitest';
import { parse } from 'yaml';
import { checkoutStress } from '@/mocks/yamls';
import { buildJourneyGraph, dropIndex, nodeKey, stepPath } from './graphLayout';
import {
  EditError,
  insertStep,
  moveStep,
  removeStep,
  updateGroup,
  updateRequest,
  updateThink,
} from './scenarioEdit';

const base = `# Shop traffic.
metadata:
  name: shop
journeys:
  - name: browse
    steps:
      # The landing page.
      - name: home
        get: /
        check: { status: 200 }
      - think: 1s..2s
      - get: /api/products
  - name: empty
load: { vus: 1, duration: 10s }
`;

type Doc = { journeys: { name: string; steps?: Record<string, unknown>[] }[] };
const doc = (y: string) => parse(y) as Doc;

describe('scenario edits', () => {
  it('moves a step and keeps comments and flow mappings', () => {
    const out = moveStep(base, ['journeys', 0, 'steps'], 0, 2);
    expect(doc(out).journeys[0]!.steps!.map((s) => s.name ?? s.get ?? s.think)).toEqual([
      '1s..2s',
      '/api/products',
      'home',
    ]);
    expect(out).toContain('# Shop traffic.');
    expect(out).toContain('# The landing page.');
    expect(out).toContain('check: { status: 200 }');
    expect(out).toContain('load: { vus: 1, duration: 10s }');
    // Moving to the same place changes nothing.
    expect(moveStep(base, ['journeys', 0, 'steps'], 1, 1)).toBe(base);
  });

  it('removes a step', () => {
    const out = removeStep(base, ['journeys', 0, 'steps'], 1);
    expect(doc(out).journeys[0]!.steps).toHaveLength(2);
    expect(out).not.toContain('think');
  });

  it('inserts request, think and group steps, creating the list when needed', () => {
    let out = insertStep(base, ['journeys', 0, 'steps'], 1, 'request');
    expect(doc(out).journeys[0]!.steps![1]).toEqual({ name: 'new request', get: '/' });
    out = insertStep(out, ['journeys', 0, 'steps'], 99, 'think');
    expect(doc(out).journeys[0]!.steps!.at(-1)).toEqual({ think: '1s' });
    out = insertStep(out, ['journeys', 1, 'steps'], 0, 'group');
    expect(doc(out).journeys[1]!.steps).toEqual([
      { group: 'new group', steps: [{ name: 'new request', get: '/' }] },
    ]);
  });

  it('changes a request in place', () => {
    const out = updateRequest(base, ['journeys', 0, 'steps', 0], {
      method: 'POST',
      url: '/api/login',
      name: 'sign in',
    });
    const step = doc(out).journeys[0]!.steps![0]!;
    expect(step).toEqual({ name: 'sign in', post: '/api/login', check: { status: 200 } });
    expect(Object.keys(step)).toEqual(['name', 'post', 'check']);
    expect(out).toContain('# The landing page.');

    // A name is added first, and an empty one removed.
    const named = updateRequest(base, ['journeys', 0, 'steps', 2], {
      method: 'GET',
      url: '/api/products?page=2',
      name: 'list',
    });
    expect(Object.keys(doc(named).journeys[0]!.steps![2]!)).toEqual(['name', 'get']);
    const unnamed = updateRequest(base, ['journeys', 0, 'steps', 0], {
      method: 'GET',
      url: '/',
      name: ' ',
    });
    expect(doc(unnamed).journeys[0]!.steps![0]).toEqual({ get: '/', check: { status: 200 } });
  });

  it('changes think durations and group names', () => {
    expect(
      doc(updateThink(base, ['journeys', 0, 'steps', 1], '3s')).journeys[0]!.steps![1],
    ).toEqual({ think: '3s' });
    const grouped = insertStep(base, ['journeys', 0, 'steps'], 0, 'group');
    const renamed = updateGroup(grouped, ['journeys', 0, 'steps', 0], 'checkout');
    expect(doc(renamed).journeys[0]!.steps![0]!.group).toBe('checkout');
  });

  it('refuses edits to broken YAML or to the wrong kind of step', () => {
    expect(() => moveStep('journeys: [', ['journeys', 0, 'steps'], 0, 1)).toThrow(EditError);
    expect(() => updateThink(base, ['journeys', 0, 'steps', 0], '1s')).toThrow(
      'That step is not a think step.',
    );
    expect(() =>
      updateRequest(base, ['journeys', 0, 'steps', 1], { method: 'GET', url: '/', name: '' }),
    ).toThrow('That step is not an HTTP request.');
    expect(() => removeStep(base, ['journeys', 0, 'steps'], 9)).toThrow('no longer exists');
  });
});

describe('scenario edits keep the file as written', () => {
  const lines = (t: string) => t.split('\n').sort();

  it('moves only the lines of the step, with the comment above it', () => {
    const out = moveStep(base, ['journeys', 0, 'steps'], 0, 2);
    expect(lines(out)).toEqual(lines(base));
    expect(out).toContain(
      '      - get: /api/products\n      # The landing page.\n      - name: home\n        get: /\n',
    );
    const big = moveStep(checkoutStress, ['journeys', 0, 'steps'], 0, 2);
    expect(lines(big)).toEqual(lines(checkoutStress));
    expect(big).toContain('tags: [stress, checkout]');
    expect(big).toContain('check: { status: [200, 201] }');
  });

  it('removes and inserts lines at the indentation of the list', () => {
    expect(removeStep(base, ['journeys', 0, 'steps'], 1)).toBe(
      base.replace('      - think: 1s..2s\n', ''),
    );
    expect(insertStep(base, ['journeys', 0, 'steps'], 1, 'think')).toBe(
      base.replace('      - think: 1s..2s\n', '      - think: 1s\n      - think: 1s..2s\n'),
    );
    expect(insertStep(base, ['journeys', 1, 'steps'], 0, 'request')).toBe(
      base.replace(
        '  - name: empty\n',
        '  - name: empty\n    steps:\n      - name: new request\n        get: /\n',
      ),
    );
  });

  it('edits a request where it is written', () => {
    const out = updateRequest(base, ['journeys', 0, 'steps', 0], {
      method: 'PUT',
      url: '/api/home?x=${rand(1, 2)}',
      name: '',
    });
    expect(out).toBe(
      base.replace(
        '      - name: home\n        get: /\n',
        '      - put: /api/home?x=${rand(1, 2)}\n',
      ),
    );
    const named = updateRequest(base, ['journeys', 0, 'steps', 2], {
      method: 'GET',
      url: '/api/products',
      name: 'list: all',
    });
    expect(named).toBe(
      base.replace(
        '      - get: /api/products\n',
        '      - name: "list: all"\n        get: /api/products\n',
      ),
    );
  });

  it('falls back to reprinting steps written in flow style', () => {
    const flow = 'journeys:\n  - name: a\n    steps: [{get: /a}, {get: /b}]\n';
    const out = moveStep(flow, ['journeys', 0, 'steps'], 0, 1);
    expect(doc(out).journeys[0]!.steps).toEqual([{ get: '/b' }, { get: '/a' }]);
    expect(doc(removeStep(base, ['journeys', 0, 'steps'], 0)).journeys[0]!.steps).toHaveLength(2);
    const only = 'journeys:\n  - name: a\n    steps:\n      - get: /a\n';
    expect(doc(removeStep(only, ['journeys', 0, 'steps'], 0)).journeys[0]!.steps).toEqual([]);
  });
});

describe('graph paths', () => {
  it('gives every step its place in the YAML', () => {
    const r = buildJourneyGraph(checkoutStress);
    if (!r.ok) throw new Error(r.error);
    const byTitle = (t: string) => r.graph.nodes.find((n) => n.title === t)!;
    expect(stepPath(byTitle('home'))).toEqual(['journeys', 0, 'steps', 0]);
    expect(stepPath(byTitle('add to cart'))).toEqual([
      'journeys',
      0,
      'steps',
      3,
      'branch',
      0,
      'steps',
      0,
    ]);
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

  it('finds where a dragged step lands among its siblings', () => {
    const r = buildJourneyGraph(base);
    if (!r.ok) throw new Error(r.error);
    const steps = r.graph.nodes.filter((n) => n.seq?.path.join('/') === 'journeys/0/steps');
    const [home, think, list] = steps;
    expect(dropIndex(steps, home!, list!.y + 10)).toBe(2);
    expect(dropIndex(steps, list!, home!.y - 10)).toBe(0);
    expect(dropIndex(steps, think!, think!.y)).toBe(1);
  });
});
