import type { Run } from '@/api/types';

export type Side = 'a' | 'b';

export interface Sides {
  side: Record<string, Side>;
  labelA: string;
  labelB: string;
}

/** The most runs on each side of a comparison (the server's limit). */
export const MAX_PER_SIDE = 20;

/**
 * Assigns selected runs to A (baseline) and B. When the runs are of exactly
 * two scenario versions, the older version is A and each side is named
 * after its version. Otherwise the older half of the runs is A.
 */
export function defaultSides(runs: Run[]): Sides {
  const sorted = [...runs].sort((x, y) => Date.parse(x.createdAt) - Date.parse(y.createdAt));
  const key = (r: Run) => `${r.scenarioId}:${r.scenarioVersion}`;
  const groups = [...new Set(sorted.map(key))];
  const side: Record<string, Side> = {};
  if (groups.length === 2) {
    const first = sorted.find((r) => key(r) === groups[0])!;
    const second = sorted.find((r) => key(r) === groups[1])!;
    // Same scenario: the lower version is the baseline. Otherwise the older runs are.
    const aKey =
      first.scenarioId === second.scenarioId && second.scenarioVersion < first.scenarioVersion
        ? groups[1]
        : groups[0];
    const aRun = aKey === groups[0] ? first : second;
    const bRun = aKey === groups[0] ? second : first;
    for (const r of sorted) side[r.id] = key(r) === aKey ? 'a' : 'b';
    const name = (r: Run) =>
      first.scenarioId === second.scenarioId
        ? `v${r.scenarioVersion}`
        : `${r.scenarioName ?? 'scenario'} v${r.scenarioVersion}`;
    return { side, labelA: name(aRun), labelB: name(bRun) };
  }
  const half = Math.ceil(sorted.length / 2);
  sorted.forEach((r, i) => (side[r.id] = i < half ? 'a' : 'b'));
  return { side, labelA: 'A', labelB: 'B' };
}
