import { useNavigate } from '@tanstack/react-router';
import { useState, type FormEvent } from 'react';
import type { Run } from '@/api/types';
import { Modal } from '@/components/dialog';
import { Button, Field, Input, Table } from '@/components/ui';
import { pct, ms, relativeTime } from '@/lib/format';
import { defaultSides, MAX_PER_SIDE, type Side } from './compareSides';

/** Assigns selected runs to A and B and opens the comparison. */
export function CompareDialog({
  projectId,
  runs,
  open,
  onOpenChange,
}: {
  projectId: string;
  runs: Run[];
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const navigate = useNavigate();
  const [initial] = useState(() => defaultSides(runs));
  const [side, setSide] = useState<Record<string, Side>>(initial.side);
  const [labelA, setLabelA] = useState(initial.labelA);
  const [labelB, setLabelB] = useState(initial.labelB);
  const sorted = [...runs].sort((x, y) => Date.parse(x.createdAt) - Date.parse(y.createdAt));
  const a = sorted.filter((r) => side[r.id] === 'a').map((r) => r.id);
  const b = sorted.filter((r) => side[r.id] === 'b').map((r) => r.id);
  const error =
    a.length === 0 || b.length === 0
      ? 'Put at least one run in A and one in B.'
      : a.length > MAX_PER_SIDE || b.length > MAX_PER_SIDE
        ? `At most ${MAX_PER_SIDE} runs on each side.`
        : undefined;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (error) return;
    onOpenChange(false);
    void navigate({
      to: '/projects/$projectId/compare',
      params: { projectId },
      search: {
        a,
        b,
        ...(labelA.trim() ? { labelA: labelA.trim() } : {}),
        ...(labelB.trim() ? { labelB: labelB.trim() } : {}),
      },
    });
  };

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Compare runs"
      description="A is the baseline and B the new version. Use at least three runs of each: with fewer, changes cannot be told apart from noise."
      width="max-w-2xl"
      footer={
        <>
          {error && <span className="mr-auto text-xs text-fail">{error}</span>}
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button variant="primary" type="submit" form="compare-form" disabled={!!error}>
            Compare
          </Button>
        </>
      }
    >
      <form id="compare-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Name for A" hint={`${a.length} run${a.length === 1 ? '' : 's'}`}>
            {(p) => (
              <Input
                {...p}
                maxLength={100}
                value={labelA}
                onChange={(e) => setLabelA(e.target.value)}
              />
            )}
          </Field>
          <Field label="Name for B" hint={`${b.length} run${b.length === 1 ? '' : 's'}`}>
            {(p) => (
              <Input
                {...p}
                maxLength={100}
                value={labelB}
                onChange={(e) => setLabelB(e.target.value)}
              />
            )}
          </Field>
        </div>
        <div className="rounded-md border border-line">
          <Table>
            <thead>
              <tr>
                <th>Run</th>
                <th className="!text-right">p95</th>
                <th className="!text-right">Errors</th>
                <th className="!text-center">A</th>
                <th className="!text-center">B</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((r) => {
                const short = r.id.slice(0, 8);
                return (
                  <tr key={r.id}>
                    <td>
                      <span className="font-medium">{r.scenarioName ?? 'scenario'}</span>
                      <span className="num ml-1.5 text-xs text-muted">v{r.scenarioVersion}</span>
                      <span className="ml-2 font-mono text-xs text-muted">{short}</span>
                      <div className="text-xs text-muted">
                        {relativeTime(r.startedAt ?? r.createdAt)}
                        {r.note ? ` · ${r.note}` : ''}
                      </div>
                    </td>
                    <td className="num text-right">{ms(r.summary?.p95)}</td>
                    <td className="num text-right">{pct(r.summary?.errorRate)}</td>
                    {(['a', 'b'] as const).map((s) => (
                      <td key={s} className="text-center">
                        <input
                          type="radio"
                          name={`side-${r.id}`}
                          aria-label={`Run ${short} in ${s.toUpperCase()}`}
                          checked={side[r.id] === s}
                          onChange={() => setSide((x) => ({ ...x, [r.id]: s }))}
                        />
                      </td>
                    ))}
                  </tr>
                );
              })}
            </tbody>
          </Table>
        </div>
      </form>
    </Modal>
  );
}
