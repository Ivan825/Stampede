import { Link, useNavigate } from '@tanstack/react-router';
import type { Run } from '@/api/types';
import { StatusChip, VerdictChip } from '@/components/chips';
import { Table } from '@/components/ui';
import { clsx } from 'clsx';
import { humanDuration, ms, pct, rate, relativeTime, dateTime } from '@/lib/format';

function runDuration(r: Run): number | null {
  if (!r.startedAt) return null;
  const end = r.endedAt ? Date.parse(r.endedAt) : Date.now();
  return (end - Date.parse(r.startedAt)) / 1000;
}

export function RunsTable({ runs, showScenario = true }: { runs: Run[]; showScenario?: boolean }) {
  const navigate = useNavigate();
  return (
    <Table>
      <thead>
        <tr>
          <th>Status</th>
          {showScenario && <th>Scenario</th>}
          <th>Target</th>
          <th>Verdict</th>
          <th className="!text-right">p95</th>
          <th className="!text-right">Errors</th>
          <th className="!text-right">Req/s</th>
          <th>Started</th>
          <th className="!text-right">Duration</th>
        </tr>
      </thead>
      <tbody>
        {runs.map((r) => {
          const s = r.summary;
          return (
            <tr
              key={r.id}
              className="cursor-pointer hover:bg-surface-2/60"
              onClick={(e) => {
                if ((e.target as HTMLElement).closest('a')) return;
                void navigate({ to: '/runs/$runId', params: { runId: r.id } });
              }}
            >
              <td>
                <StatusChip status={r.status} />
              </td>
              {showScenario && (
                <td className="max-w-64">
                  <Link
                    to="/runs/$runId"
                    params={{ runId: r.id }}
                    className="font-medium hover:underline"
                  >
                    {r.scenarioName ?? 'scenario'}
                  </Link>
                  <span className="num ml-1.5 text-xs text-muted">v{r.scenarioVersion}</span>
                  {r.note && <div className="truncate text-xs text-muted">{r.note}</div>}
                </td>
              )}
              <td className="max-w-56 truncate font-mono text-xs text-muted">{r.targetURL}</td>
              <td>
                <VerdictChip verdict={r.verdict} />
              </td>
              <td className="num text-right">{ms(s?.p95)}</td>
              <td
                className={clsx(
                  'num text-right',
                  s?.errorRate != null && s.errorRate >= 0.01 && 'text-fail',
                )}
              >
                {pct(s?.errorRate)}
              </td>
              <td className="num text-right">{rate(s?.rps)}</td>
              <td
                className="text-xs whitespace-nowrap text-muted"
                title={dateTime(r.startedAt ?? r.createdAt)}
              >
                {relativeTime(r.startedAt ?? r.createdAt)}
              </td>
              <td className="num text-right text-xs text-muted">{humanDuration(runDuration(r))}</td>
            </tr>
          );
        })}
      </tbody>
    </Table>
  );
}
