import { useState } from 'react';
import { useNotificationChannels, useNotificationDeliveries } from '@/api/queries';
import type { NotificationChannel, NotificationDelivery, NotificationKind } from '@/api/types';
import { Chip } from '@/components/chips';
import { CliHint } from '@/components/cliHint';
import { Modal } from '@/components/dialog';
import { Button, Card, CardHeader, EmptyState, ErrorAlert, Loading, Table } from '@/components/ui';
import { cli } from '@/lib/cli';
import { dateTime, relativeTime } from '@/lib/format';

const kindLabels: Record<NotificationKind, string> = {
  webhook: 'Webhook',
  slack: 'Slack',
  discord: 'Discord',
};

/** A delivery attempt's outcome as a chip. */
export function DeliveryChip({ d }: { d: NotificationDelivery }) {
  return d.ok ? (
    <Chip tone="pass">{d.statusCode || 'ok'}</Chip>
  ) : (
    <Chip tone="fail" title={d.error}>
      {d.statusCode ? d.statusCode : 'failed'}
    </Chip>
  );
}

function DeliveryLog({ channel, onClose }: { channel: NotificationChannel; onClose: () => void }) {
  const log = useNotificationDeliveries(channel.id);
  return (
    <Modal
      open
      onOpenChange={(v) => !v && onClose()}
      width="max-w-3xl"
      title={`Deliveries · ${channel.name}`}
      description="The most recent attempts, newest first. Failed deliveries are retried with backoff."
      footer={<Button onClick={onClose}>Close</Button>}
    >
      {log.isPending ? (
        <Loading />
      ) : log.error ? (
        <ErrorAlert error={log.error} />
      ) : log.data.length === 0 ? (
        <EmptyState title="Nothing delivered yet" />
      ) : (
        <Table aria-label="Deliveries">
          <thead>
            <tr>
              <th>When</th>
              <th>Event</th>
              <th>Attempt</th>
              <th>Result</th>
              <th>Took</th>
              <th>Error</th>
            </tr>
          </thead>
          <tbody>
            {log.data.map((d) => (
              <tr key={d.id}>
                <td className="num text-xs whitespace-nowrap text-muted" title={d.at}>
                  {dateTime(d.at)}
                </td>
                <td className="font-mono text-xs">{d.event}</td>
                <td className="num">{d.attempt}</td>
                <td>
                  <DeliveryChip d={d} />
                </td>
                <td className="num text-xs text-muted">{d.durationMs} ms</td>
                <td className="max-w-64 truncate text-xs text-muted" title={d.error}>
                  {d.error}
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Modal>
  );
}

/** Notification channels and their deliveries, read-only. */
export function NotificationsTab() {
  const list = useNotificationChannels();
  const [logFor, setLogFor] = useState<NotificationChannel | null>(null);
  return (
    <Card>
      <CardHeader
        title="Notifications"
        description="Webhooks, Slack and Discord channels told when runs finish, miss a target or are killed."
        actions={<CliHint command={cli.notifyChannelsCreate}>Add a channel</CliHint>}
      />
      {list.isPending ? (
        <Loading />
      ) : list.error ? (
        <ErrorAlert error={list.error} className="m-4" />
      ) : list.data.length === 0 ? (
        <EmptyState title="No notification channels" />
      ) : (
        <Table aria-label="Notification channels">
          <thead>
            <tr>
              <th>Name</th>
              <th>Kind</th>
              <th>Destination</th>
              <th>Events</th>
              <th>Last delivery</th>
              <th>
                <span className="sr-only">Deliveries</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {list.data.map((c) => (
              <tr key={c.id}>
                <td className="font-mono text-xs font-medium">{c.name}</td>
                <td>
                  <Chip tone="info">{kindLabels[c.kind]}</Chip>
                </td>
                <td className="font-mono text-xs text-muted">
                  {c.urlHint}
                  {c.allowPrivate && (
                    <Chip tone="warn" className="ml-2">
                      private allowed
                    </Chip>
                  )}
                  {c.hasSecret && <span className="ml-2 text-muted">signed</span>}
                </td>
                <td className="font-mono text-xs text-muted">{c.events.join(', ')}</td>
                <td>
                  {c.lastDelivery ? (
                    <span className="flex items-center gap-2 text-xs text-muted">
                      <DeliveryChip d={c.lastDelivery} />
                      {relativeTime(c.lastDelivery.at)}
                    </span>
                  ) : (
                    <span className="text-xs text-muted">never</span>
                  )}
                </td>
                <td className="text-right">
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={`Deliveries of ${c.name}`}
                    onClick={() => setLogFor(c)}
                  >
                    Deliveries
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      {logFor && <DeliveryLog channel={logFor} onClose={() => setLogFor(null)} />}
    </Card>
  );
}
