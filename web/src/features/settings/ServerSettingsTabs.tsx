import type { ReactNode } from 'react';
import { useLimitSettings, useSSOSettings } from '@/api/queries';
import type { LimitCaps } from '@/api/types';
import { Chip, RoleChip } from '@/components/chips';
import { Card, CardHeader, ErrorAlert, Loading, Notice, Table } from '@/components/ui';
import { humanDuration, pct } from '@/lib/format';

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="contents">
      <dt className="text-muted">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  );
}

/** The server's OIDC configuration, read-only; the client secret is never shown. */
export function SSOTab() {
  const sso = useSSOSettings();
  if (sso.isPending) return <Loading />;
  if (sso.error) return <ErrorAlert error={sso.error} />;
  const s = sso.data;
  return (
    <Card>
      <CardHeader
        title="Single sign-on"
        description="Read-only. Set with the stampede server --oidc-* flags or STAMPEDE_OIDC_* variables."
        actions={<Chip tone={s.enabled ? 'pass' : 'neutral'}>{s.enabled ? 'enabled' : 'off'}</Chip>}
      />
      {s.enabled ? (
        <dl className="grid grid-cols-[11rem_1fr] gap-y-2 px-4 py-3 text-[13px]">
          <Row label="Sign-in button">{s.name}</Row>
          <Row label="Issuer">
            <span className="font-mono text-xs">{s.issuer}</span>
          </Row>
          <Row label="Redirect URL">
            <span className="font-mono text-xs">{s.redirectURL}</span>
          </Row>
          <Row label="Allowed email domains">
            {s.allowedDomains.length ? (
              <span className="flex flex-wrap gap-1">
                {s.allowedDomains.map((d) => (
                  <Chip key={d}>{d}</Chip>
                ))}
              </span>
            ) : (
              'Any domain'
            )}
          </Row>
          <Row label="First sign-in">
            {s.defaultRole ? (
              <span className="flex items-center gap-1.5">
                Creates an account with the role <RoleChip role={s.defaultRole} />
              </span>
            ) : (
              'Only people who already have an account may sign in'
            )}
          </Row>
          <Row label="Scopes">
            <span className="font-mono text-xs">{s.scopes.join(' ')}</span>
          </Row>
          <Row label="Password sign-in">{s.passwordLogin ? 'Also available' : 'Off'}</Row>
        </dl>
      ) : (
        <p className="px-4 py-3 text-[13px] text-muted">
          Single sign-on is not configured; people sign in with email and password. To add it, start
          the server with --oidc-issuer and --oidc-client-id, and the client secret in
          STAMPEDE_OIDC_CLIENT_SECRET.
        </p>
      )}
    </Card>
  );
}

const capCells = (c: LimitCaps) => [
  c.maxRate != null ? `${c.maxRate.toLocaleString('en-US')}/s` : 'no cap',
  c.maxVUs != null ? c.maxVUs.toLocaleString('en-US') : 'no cap',
  c.maxDurationSeconds != null ? humanDuration(c.maxDurationSeconds) : 'no cap',
];

function CapsRow({ label, caps, note }: { label: string; caps: LimitCaps; note?: string }) {
  return (
    <tr>
      <td>
        <div className="font-medium">{label}</div>
        {note && <div className="text-xs text-muted">{note}</div>}
      </td>
      {capCells(caps).map((v, i) => (
        <td key={i} className={v === 'no cap' ? 'text-right text-muted' : 'num text-right'}>
          {v}
        </td>
      ))}
    </tr>
  );
}

const capHeads = (
  <>
    <th className="!text-right">Rate</th>
    <th className="!text-right">Virtual users</th>
    <th className="!text-right">Duration</th>
  </>
);

/** The caps every run is checked against, and each target's caps. */
export function LimitsTab() {
  const limits = useLimitSettings();
  if (limits.isPending) return <Loading />;
  if (limits.error) return <ErrorAlert error={limits.error} />;
  const l = limits.data;
  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader
          title="Server caps"
          description="Read-only. A run must fit every cap that applies to it; the tightest wins."
        />
        <Table aria-label="Server caps">
          <thead>
            <tr>
              <th>Applies to</th>
              {capHeads}
            </tr>
          </thead>
          <tbody>
            <CapsRow
              label="Every run"
              caps={l.server}
              note="stampede server --max-rate, --max-vus, --max-duration"
            />
            <CapsRow
              label="Unverified public targets"
              caps={l.unverifiedPublic}
              note="Lifted once the target's ownership is verified."
            />
          </tbody>
        </Table>
      </Card>
      <Card>
        <CardHeader title="Abort floor" />
        {l.abortFloor ? (
          <p className="px-4 py-3 text-[13px]">
            Any run whose{' '}
            {[
              l.abortFloor.errorRate != null &&
                `error rate stays at or above ${pct(l.abortFloor.errorRate, 0)}`,
              l.abortFloor.p95Seconds != null &&
                `p95 latency stays above ${Math.round(l.abortFloor.p95Seconds * 1000)}ms`,
            ]
              .filter(Boolean)
              .join(' or ')}{' '}
            for {humanDuration(l.abortFloor.forSeconds)} is stopped, even when its scenario sets no
            abort limits.
          </p>
        ) : (
          <Notice tone="warn" className="m-3">
            Off: only a scenario's own abort limits stop a failing run (stampede server
            --abort-errors 0).
          </Notice>
        )}
      </Card>
      <Card>
        <CardHeader
          title="Targets"
          description="Each target's own caps, and the effective caps after the server's caps apply."
        />
        {l.targets.length === 0 ? (
          <p className="px-4 py-3 text-[13px] text-muted">No targets yet.</p>
        ) : (
          <Table aria-label="Target caps">
            <thead>
              <tr>
                <th>Target</th>
                <th>Project</th>
                <th>Ownership</th>
                <th className="!text-right">Own caps</th>
                {capHeads}
              </tr>
            </thead>
            <tbody>
              {l.targets.map((t) => {
                const own = capCells(t.caps).filter((v) => v !== 'no cap');
                return (
                  <tr key={t.id}>
                    <td>
                      <div className="font-medium">{t.name}</div>
                      <div className="font-mono text-xs text-muted">{t.baseURL}</div>
                    </td>
                    <td>{t.projectName}</td>
                    <td>
                      {t.private ? (
                        <Chip>private</Chip>
                      ) : t.verified ? (
                        <Chip tone="pass">verified</Chip>
                      ) : (
                        <Chip tone="warn">unverified</Chip>
                      )}
                    </td>
                    <td className="text-right text-xs text-muted">
                      {own.length ? own.join(' · ') : 'none'}
                    </td>
                    {capCells(t.effective).map((v, i) => (
                      <td
                        key={i}
                        className={v === 'no cap' ? 'text-right text-muted' : 'num text-right'}
                      >
                        {v}
                      </td>
                    ))}
                  </tr>
                );
              })}
            </tbody>
          </Table>
        )}
      </Card>
    </div>
  );
}
