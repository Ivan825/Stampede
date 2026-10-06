import { Link } from '@tanstack/react-router';
import { OctagonX } from 'lucide-react';
import { useActiveRuns, useKillAll, useMe } from '@/api/queries';
import { StatusChip } from '@/components/chips';
import { Confirm } from '@/components/dialog';
import { Button } from '@/components/ui';
import { useToast } from '@/components/toast';
import { permissions } from '@/lib/roles';

/**
 * The organisation-wide kill switch. Always in the header while any run is
 * active; stops all load at once after a confirmation.
 */
export function KillSwitch() {
  const me = useMe();
  const active = useActiveRuns();
  const killAll = useKillAll();
  const toast = useToast();
  const runs = active.data ?? [];
  if (runs.length === 0) return null;
  const allowed = permissions(me.role).stopRuns;
  const label = `${runs.length} active run${runs.length === 1 ? '' : 's'}`;

  return (
    <Confirm
      trigger={
        <Button
          variant="danger-solid"
          size="sm"
          disabled={!allowed}
          title={allowed ? undefined : 'Your role cannot stop runs'}
          aria-label={`Kill switch: stop all ${label}`}
        >
          <OctagonX className="size-4" aria-hidden />
          Kill all
          <span className="num rounded bg-black/20 px-1.5 py-0.5 text-[11px]">{runs.length}</span>
        </Button>
      }
      title="Stop all load now?"
      destructive
      confirmLabel={`Kill ${label}`}
      description={
        <div className="flex flex-col gap-3">
          <p>
            Every active run in the organisation stops immediately. In-flight requests are abandoned
            and the runs end as aborted.
          </p>
          <ul className="flex flex-col gap-1.5 rounded-md border border-line p-2">
            {runs.map((r) => (
              <li key={r.id} className="flex items-center gap-2 text-fg">
                <StatusChip status={r.status} />
                <Link
                  to="/runs/$runId"
                  params={{ runId: r.id }}
                  className="truncate hover:underline"
                >
                  {r.scenarioName ?? r.scenarioId}
                </Link>
                <span className="ml-auto truncate font-mono text-xs text-muted">{r.targetURL}</span>
              </li>
            ))}
          </ul>
        </div>
      }
      onConfirm={async () => {
        const res = await killAll.mutateAsync();
        toast.success(`Killed ${res.killed.length} run${res.killed.length === 1 ? '' : 's'}.`);
      }}
    />
  );
}
