import { useParams } from '@tanstack/react-router';
import { useState } from 'react';
import { useTargets } from '@/api/queries';
import type { Caps, Target } from '@/api/types';
import { CliHint } from '@/components/cliHint';
import { CopyButton } from '@/components/misc';
import { Button, Card, EmptyState, ErrorAlert, Loading, PageHeader } from '@/components/ui';
import { TargetBadge } from '@/features/targets/TargetBadge';
import { cli } from '@/lib/cli';
import { dateTime, humanDuration, num } from '@/lib/format';

function capsText(c: Caps | undefined): string {
  if (!c) return 'none';
  const parts = [
    c.maxRate != null && `≤ ${num(c.maxRate)}/s`,
    c.maxVUs != null && `≤ ${c.maxVUs} VUs`,
    c.maxDurationSeconds != null && `≤ ${humanDuration(c.maxDurationSeconds)}`,
  ].filter(Boolean);
  return parts.length ? parts.join(' · ') : 'none';
}

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

function Verification({ target }: { target: Target }) {
  const host = hostOf(target.baseURL);
  const record = `stampede-verify=${target.verificationToken}`;
  let origin = target.baseURL;
  try {
    origin = new URL(target.baseURL).origin;
  } catch {
    /* keep as typed */
  }
  return (
    <div className="flex flex-col gap-3 border-t border-line bg-surface-2/40 px-4 py-3 text-[13px]">
      <p>
        Prove you control <span className="font-mono">{host}</span> with either method, then check
        from the terminal:
      </p>
      <div className="grid gap-3 md:grid-cols-2">
        <div className="rounded-md border border-line bg-surface p-3">
          <p className="label-caps mb-1.5">DNS TXT record</p>
          <p className="mb-1 text-xs text-muted">
            Add a TXT record on <span className="font-mono">{host.split(':')[0]}</span>:
          </p>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded bg-surface-2 px-2 py-1 font-mono text-xs">
              {record}
            </code>
            <CopyButton value={record} />
          </div>
        </div>
        <div className="rounded-md border border-line bg-surface p-3">
          <p className="label-caps mb-1.5">Well-known file</p>
          <p className="mb-1 text-xs text-muted">
            Serve this token at{' '}
            <span className="font-mono">{origin}/.well-known/stampede-verify.txt</span>:
          </p>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded bg-surface-2 px-2 py-1 font-mono text-xs">
              {target.verificationToken}
            </code>
            <CopyButton value={target.verificationToken} />
          </div>
        </div>
      </div>
      <CliHint command={cli.targetsVerify(target.name)} className="self-start">
        Check the token
      </CliHint>
    </div>
  );
}

export function TargetsPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/targets' });
  const targets = useTargets(projectId);
  const [expanded, setExpanded] = useState<string | null>(null);

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        title="Targets"
        description="Where load goes. Each run is limited to its target's host and caps."
        actions={<CliHint command={cli.targetsCreate}>Add a target</CliHint>}
      />
      {targets.isPending ? (
        <Loading />
      ) : targets.error ? (
        <ErrorAlert error={targets.error} />
      ) : targets.data.length === 0 ? (
        <Card>
          <EmptyState title="No targets">
            Add a target for each environment you test, such as local, staging or production, with{' '}
            <code className="font-mono text-xs">stampede targets create</code>.
          </EmptyState>
        </Card>
      ) : (
        <div className="flex flex-col gap-3">
          {targets.data.map((t) => {
            const needsVerify = !t.private && !t.verified;
            const open = expanded === t.id || (needsVerify && expanded === null);
            return (
              <Card key={t.id}>
                <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="font-medium">{t.name}</span>
                      <TargetBadge target={t} />
                    </div>
                    <div className="mt-0.5 truncate font-mono text-xs text-muted">{t.baseURL}</div>
                  </div>
                  <dl className="grid grid-cols-[auto_auto] gap-x-3 gap-y-0.5 text-xs">
                    <dt className="text-muted">Caps</dt>
                    <dd className="num">{capsText(t.caps)}</dd>
                    {t.verified && t.verifiedAt && (
                      <>
                        <dt className="text-muted">Verified</dt>
                        <dd>
                          {dateTime(t.verifiedAt)}
                          {t.verificationMethod ? ` via ${t.verificationMethod}` : ''}
                        </dd>
                      </>
                    )}
                    {t.allowHosts && t.allowHosts.length > 0 && (
                      <>
                        <dt className="text-muted">Also allows</dt>
                        <dd className="font-mono">{t.allowHosts.join(', ')}</dd>
                      </>
                    )}
                  </dl>
                  {!t.private && (
                    <Button
                      size="sm"
                      variant="ghost"
                      aria-expanded={open}
                      onClick={() => setExpanded(open ? '' : t.id)}
                    >
                      {open ? 'Hide verification' : 'Verification'}
                    </Button>
                  )}
                </div>
                {!t.private && open && <Verification target={t} />}
              </Card>
            );
          })}
        </div>
      )}
    </div>
  );
}
