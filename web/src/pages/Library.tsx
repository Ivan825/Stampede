import { Link, useNavigate, useParams } from '@tanstack/react-router';
import { ChevronDown, ChevronRight, FilePlus2 } from 'lucide-react';
import { Fragment, useState } from 'react';
import { useCreateScenario, useMe, usePack, usePacks, useProjects } from '@/api/queries';
import type { PackFile } from '@/api/types';
import { Chip } from '@/components/chips';
import { CopyButton } from '@/components/misc';
import { useToast } from '@/components/toast';
import {
  Button,
  Card,
  CardHeader,
  EmptyState,
  ErrorAlert,
  Loading,
  PageHeader,
  Select,
  Table,
} from '@/components/ui';
import { lastProject } from '@/lib/lastProject';
import { permissions } from '@/lib/roles';

/** The product packs built into the server. */
export function LibraryPage() {
  const packs = usePacks();
  const list = packs.data ?? [];
  const shipped = list.filter((p) => p.status === 'shipped');
  const planned = list.filter((p) => p.status !== 'shipped');
  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        title="Library"
        description="Product packs built into this server: ready-made journeys and stress tests for one kind of product. Each shipped pack is tested against a reference app in CI."
      />
      {packs.isPending ? (
        <Loading />
      ) : packs.error ? (
        <ErrorAlert error={packs.error} />
      ) : list.length === 0 ? (
        <Card>
          <EmptyState title="No packs" />
        </Card>
      ) : (
        <>
          <ul className="grid gap-3 md:grid-cols-2 xl:grid-cols-3" aria-label="Packs">
            {shipped.map((p) => (
              <li key={p.name}>
                <Link
                  to="/library/$packName"
                  params={{ packName: p.name }}
                  className="flex h-full flex-col gap-2 rounded-lg border border-line bg-surface px-4 py-3 hover:border-line-strong"
                >
                  <span className="flex items-center gap-2">
                    <span className="flex-1 font-medium">{p.title}</span>
                    <Chip tone="pass">{p.status}</Chip>
                  </span>
                  <span className="font-mono text-xs text-muted">{p.name}</span>
                  <span className="text-[13px] text-muted">{p.signature}</span>
                  <span className="mt-auto flex flex-wrap gap-1" aria-label="Drivers">
                    {p.drivers.map((d) => (
                      <Chip key={d}>{d}</Chip>
                    ))}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
          {planned.length > 0 && (
            <Card className="mt-5">
              <CardHeader title="Planned" description="Not available yet." />
              <Table>
                <tbody>
                  {planned.map((p) => (
                    <tr key={p.name}>
                      <td className="font-medium">{p.title}</td>
                      <td className="text-xs text-muted">{p.signature}</td>
                      <td>
                        <Chip>{p.status}</Chip>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            </Card>
          )}
        </>
      )}
    </div>
  );
}

function CreateFromFile({ file }: { file: PackFile }) {
  const projects = useProjects();
  const navigate = useNavigate();
  const toast = useToast();
  const remembered = lastProject.get();
  const [projectId, setProjectId] = useState(
    () => projects.data?.find((p) => p.id === remembered)?.id ?? projects.data?.[0]?.id ?? '',
  );
  const chosen = projectId || projects.data?.[0]?.id || '';
  const create = useCreateScenario(chosen);
  if (!projects.data?.length) return null;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <Select
          aria-label={`Project for ${file.scenario}`}
          className="w-56"
          value={chosen}
          onChange={(e) => setProjectId(e.target.value)}
        >
          {projects.data.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </Select>
        <Button
          size="sm"
          loading={create.isPending}
          onClick={() =>
            create.mutate(
              { yaml: file.yaml, message: `From the ${file.path} pack file` },
              {
                onSuccess: (s) => {
                  toast.success(`Created ${s.name}.`);
                  void navigate({
                    to: '/projects/$projectId/scenarios/$scenarioId',
                    params: { projectId: chosen, scenarioId: s.id },
                  });
                },
              },
            )
          }
        >
          <FilePlus2 className="size-3.5" aria-hidden /> Create scenario
        </Button>
      </div>
      <ErrorAlert error={create.error} />
    </div>
  );
}

/** A pack's description, variables and scenario files. */
export function PackPage() {
  const { packName } = useParams({ from: '/app/library/$packName' });
  const me = useMe();
  const can = permissions(me.role);
  const pack = usePack(packName);
  const [open, setOpen] = useState<string | null>(null);
  if (pack.isPending) return <Loading />;
  if (pack.error) return <ErrorAlert error={pack.error} className="m-6" />;
  const p = pack.data;
  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        breadcrumb={
          <Link to="/library" className="hover:underline">
            Library
          </Link>
        }
        title={p.title}
        description={p.description}
        actions={
          <span className="flex flex-wrap gap-1">
            {p.protocols.map((x) => (
              <Chip key={x}>{x}</Chip>
            ))}
          </span>
        }
      />
      <dl className="mb-5 grid grid-cols-[9rem_1fr] gap-y-1.5 text-[13px]">
        <dt className="text-muted">Name</dt>
        <dd className="font-mono">{p.name}</dd>
        <dt className="text-muted">Status</dt>
        <dd>{p.status}</dd>
        {p.referenceApp && (
          <>
            <dt className="text-muted">Reference app</dt>
            <dd className="font-mono">{p.referenceApp}</dd>
          </>
        )}
        <dt className="text-muted">Install</dt>
        <dd className="font-mono text-xs">stampede pack install {p.name}</dd>
      </dl>
      {p.variables.length > 0 && (
        <Card className="mb-5">
          <CardHeader
            title="Variables"
            description="Set as environment variables (${env.NAME}) when running."
          />
          <Table>
            <tbody>
              {p.variables.map((v) => (
                <tr key={v.name}>
                  <td className="w-48 font-mono text-xs">{v.name}</td>
                  <td className="text-muted">{v.description}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
      <Card>
        <CardHeader title="Scenario files" description={`${p.files.length} files`} />
        <Table aria-label="Scenario files">
          <thead>
            <tr>
              <th>File</th>
              <th>Kind</th>
              <th>Scenario</th>
              <th>Journeys</th>
              <th>Shape</th>
            </tr>
          </thead>
          <tbody>
            {p.files.map((f) => {
              const expanded = open === f.path;
              const id = `pack-file-${f.path.replace(/[^a-z0-9]/gi, '-')}`;
              return (
                <Fragment key={f.path}>
                  <tr>
                    <td>
                      <button
                        type="button"
                        className="inline-flex items-center gap-1 font-mono text-xs text-info hover:underline"
                        aria-expanded={expanded}
                        aria-controls={id}
                        onClick={() => setOpen(expanded ? null : f.path)}
                      >
                        {expanded ? (
                          <ChevronDown className="size-3.5" aria-hidden />
                        ) : (
                          <ChevronRight className="size-3.5" aria-hidden />
                        )}
                        {f.path}
                      </button>
                      {f.description && (
                        <p className="mt-0.5 max-w-md text-xs text-muted">{f.description}</p>
                      )}
                    </td>
                    <td>
                      <Chip tone={f.kind === 'stress' ? 'warn' : 'info'}>{f.kind}</Chip>
                    </td>
                    <td className="font-mono text-xs">{f.scenario}</td>
                    <td>
                      <span className="flex flex-wrap gap-1">
                        {f.journeys.map((j) => (
                          <Chip key={j}>{j}</Chip>
                        ))}
                      </span>
                    </td>
                    <td className="font-mono text-xs">{f.shape ?? '–'}</td>
                  </tr>
                  {expanded && (
                    <tr id={id}>
                      <td colSpan={5} className="bg-surface-2/50">
                        <div className="flex flex-col gap-2 py-1">
                          <div className="flex flex-wrap items-start gap-2">
                            <CopyButton value={f.yaml} label="Copy YAML" />
                            {can.editScenarios && <CreateFromFile file={f} />}
                          </div>
                          <pre
                            className="max-h-96 overflow-auto rounded-md border border-line bg-bg p-3 font-mono text-xs leading-relaxed"
                            aria-label={`${f.path} YAML`}
                          >
                            {f.yaml}
                          </pre>
                        </div>
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </tbody>
        </Table>
      </Card>
    </div>
  );
}
