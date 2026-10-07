/**
 * The stampede CLI commands the web UI points to. The web UI only reads and
 * reports; every change is made from the terminal. Kept in one place so the
 * hints match the CLI.
 */

/** Quotes an argument for a POSIX shell when it needs it; <placeholders> stay as they are. */
export function shellArg(s: string): string {
  if (/^<[\w.-]+>$/.test(s)) return s;
  return /^[\w@%+=:,./-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`;
}

export const cli = {
  setup: 'stampede setup',
  login: 'stampede login',
  password: 'stampede password',
  projectsCreate: 'stampede projects create <name>',
  projectSettings: (project: string) =>
    `stampede projects settings set --project ${shellArg(project)} --max-rate <n> --require-dry-run`,
  projectRoles: (project: string) =>
    `stampede projects roles set <email> <role> --project ${shellArg(project)}`,
  targetsCreate: 'stampede targets create <name> --base-url <url>',
  targetsVerify: (target: string) => `stampede targets verify ${shellArg(target)}`,
  secretsSet: (project: string) => `stampede secrets set <NAME> --project ${shellArg(project)}`,
  push: (project?: string) =>
    `stampede push${project ? ` --project ${shellArg(project)}` : ''} <scenario.yaml>`,
  start: ({
    project,
    scenario = '<scenario>',
    target = '<target>',
  }: { project?: string; scenario?: string; target?: string } = {}) =>
    `stampede start${project ? ` --project ${shellArg(project)}` : ''} --scenario ${shellArg(scenario)} --target ${shellArg(target)}`,
  stop: (run: string) => `stampede stop ${run}`,
  kill: (run: string) => `stampede kill ${run}`,
  killAll: 'stampede kill --all',
  schedulesCreate:
    'stampede schedules create <name> --scenario <scenario> --target <target> --cron "<cron>"',
  schedulesUpdate: (name: string) => `stampede schedules update ${shellArg(name)}`,
  schedulesRun: (name: string) => `stampede schedules run ${shellArg(name)}`,
  driftRepair: (id: string) => `stampede drift repair ${id}`,
  aiJobsCreate: 'stampede ai jobs create --describe "<what users do>" --target <url>',
  aiJobsApprove: (id: string) => `stampede ai jobs approve ${id}`,
  aiProvidersSet: 'stampede ai providers set <name> --kind <kind> --api-key-env <VAR>',
  narrative: (run: string) => `stampede narrative ${run}`,
  usersCreate: 'stampede users create <email> --role <role>',
  tokensCreate: 'stampede tokens create <name> --role <role>',
  integrationsCreate: 'stampede integrations create <name> --kind <kind> --url <url>',
  notifyChannelsCreate: 'stampede notify channels create <name> --kind <kind> --url-env <VAR>',
  capsSet: 'stampede caps set --max-rate <n> --max-vus <n> --max-duration <d>',
  packInstall: (pack: string) => `stampede pack install ${shellArg(pack)}`,
  coverage: 'stampede coverage <scenario.yaml> --from-openapi <openapi.yaml>',
  driftDryRun: 'stampede drift <scenario.yaml> --from-openapi <openapi.yaml> --target <url>',
} as const;
