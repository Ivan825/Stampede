import { Link } from '@tanstack/react-router';
import { useWorkers } from '@/api/queries';
import type { Worker } from '@/api/types';
import { Chip, WorkerStatusChip } from '@/components/chips';
import { Card, EmptyState, ErrorAlert, Loading, PageHeader, Stat, Table } from '@/components/ui';
import { bytes, dateTime, relativeTime } from '@/lib/format';

const order: Record<Worker['status'], number> = { saturated: 0, busy: 1, idle: 2, lost: 3 };

export function WorkersPage() {
  const workers = useWorkers();
  const list = [...(workers.data ?? [])].sort(
    (a, b) => order[a.status] - order[b.status] || a.name.localeCompare(b.name),
  );
  const countBy = (s: Worker['status']) => list.filter((w) => w.status === s).length;
  const cpus = list.filter((w) => w.status !== 'lost').reduce((n, w) => n + (w.cpus ?? 0), 0);

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        title="Workers"
        description="Load generators connected to this server. Refreshes every few seconds."
      />
      {workers.data && (
        <div className="mb-5 grid grid-cols-2 gap-3 md:grid-cols-5">
          <Stat label="connected" value={list.length - countBy('lost')} />
          <Stat label="idle" value={countBy('idle')} />
          <Stat label="busy" value={countBy('busy')} />
          <Stat
            label="saturated"
            value={countBy('saturated')}
            tone={countBy('saturated') ? 'warn' : undefined}
          />
          <Stat label="CPUs available" value={cpus} />
        </div>
      )}
      <Card>
        {workers.isPending ? (
          <Loading />
        ) : workers.error ? (
          <ErrorAlert error={workers.error} className="m-4" />
        ) : list.length === 0 ? (
          <EmptyState title="No workers connected">
            Start a worker with <code className="font-mono">stampede worker --server …</code>. The
            server can also run load in-process.
          </EmptyState>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Status</th>
                <th>Region</th>
                <th>Labels</th>
                <th>Plugins</th>
                <th className="!text-right">CPUs</th>
                <th className="!text-right">Memory</th>
                <th>Version</th>
                <th>Run</th>
                <th>Last seen</th>
              </tr>
            </thead>
            <tbody>
              {list.map((w) => (
                <tr key={w.id}>
                  <td>
                    <div className="font-medium">{w.name}</div>
                    <div className="font-mono text-[11px] text-muted">{w.id}</div>
                  </td>
                  <td>
                    <WorkerStatusChip status={w.status} />
                  </td>
                  <td className="font-mono text-xs">{w.region}</td>
                  <td>
                    <div className="flex max-w-64 flex-wrap gap-1">
                      {Object.entries(w.labels ?? {}).map(([k, v]) => (
                        <Chip key={k}>
                          {k}={v}
                        </Chip>
                      ))}
                    </div>
                  </td>
                  <td>
                    <div className="flex max-w-48 flex-wrap gap-1">
                      {(w.plugins ?? []).length === 0 ? (
                        <span className="text-muted">–</span>
                      ) : (
                        (w.plugins ?? []).map((p) => <Chip key={p}>{p}</Chip>)
                      )}
                    </div>
                  </td>
                  <td className="num text-right">{w.cpus ?? '–'}</td>
                  <td className="num text-right">{bytes(w.memoryBytes)}</td>
                  <td className="font-mono text-xs text-muted">{w.version ?? '–'}</td>
                  <td>
                    {w.runId ? (
                      <Link
                        to="/runs/$runId"
                        params={{ runId: w.runId }}
                        className="font-mono text-xs text-info hover:underline"
                      >
                        {w.runId.slice(0, 8)}
                      </Link>
                    ) : (
                      <span className="text-muted">–</span>
                    )}
                  </td>
                  <td
                    className="text-xs whitespace-nowrap text-muted"
                    title={dateTime(w.lastSeenAt)}
                  >
                    {relativeTime(w.lastSeenAt)}
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
    </div>
  );
}
