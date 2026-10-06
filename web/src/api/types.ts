import type { components } from './schema.gen';

type S = components['schemas'];

export type ApiErrorBody = S['Error'];
export type VersionInfo = S['VersionInfo'];
export type Role = S['Role'];
export type SetupRequest = S['SetupRequest'];
export type LoginRequest = S['LoginRequest'];
export type Session = S['Session'];
export type Me = S['Me'];
export type User = S['User'];
export type UserCreate = S['UserCreate'];
export type Token = S['Token'];
export type TokenCreate = S['TokenCreate'];
export type TokenCreated = S['TokenCreated'];
export type Project = S['Project'];
export type ProjectCreate = S['ProjectCreate'];
export type Caps = S['Caps'];
export type Target = S['Target'];
export type TargetCreate = S['TargetCreate'];
export type Secret = S['Secret'];
export type SecretPut = S['SecretPut'];
export type Validation = S['Validation'];
export type PlanSummary = S['PlanSummary'];
export type Scenario = S['Scenario'];
export type ScenarioVersion = S['ScenarioVersion'];
export type ScenarioVersionCreate = S['ScenarioVersionCreate'];
export type RunStatus = S['RunStatus'];
export type RunOverrides = S['RunOverrides'];
export type RunCreate = S['RunCreate'];
export type Run = S['Run'];
export type RunSummary = S['RunSummary'];
export type Point = S['Point'];
export type Worker = S['Worker'];
export type AuditEntry = S['AuditEntry'];

export type Verdict = NonNullable<Run['verdict']>;

export const activeStatuses: readonly RunStatus[] = [
  'scheduling',
  'starting',
  'running',
  'stopping',
  'analyzing',
];

export const terminalStatuses: readonly RunStatus[] = ['completed', 'aborted', 'failed'];

export function isActive(status: RunStatus): boolean {
  return activeStatuses.includes(status);
}

export function isTerminal(status: RunStatus): boolean {
  return terminalStatuses.includes(status);
}

/** A worker or safety event from the live stream. */
export interface RunEvent {
  type: string;
  message: string;
  worker?: string;
  at: string;
}

// The report JSON. The OpenAPI schema leaves it open
// (additionalProperties: true); these types follow internal/report/report.go.

export interface Percentiles {
  count: number;
  min: number;
  mean: number;
  p50: number;
  p90: number;
  p95: number;
  p99: number;
  p999: number;
  max: number;
}

export interface Stats {
  requests: number;
  failed: number;
  errorRate: number;
  rps: number;
  latency: Percentiles;
  service: Percentiles;
  bytesIn: number;
  bytesOut: number;
  checksPassed: number;
  checksFailed: number;
  status?: Record<string, number>;
  iterations?: number;
  iterationsFailed?: number;
  iterationTime?: Percentiles;
  dropped?: number;
}

export interface PhaseStat {
  mean: number;
  p95: number;
}

export interface ReportStep {
  id: number;
  name: string;
  stats: Stats;
  phases: Record<string, PhaseStat>;
}

export interface ReportJourney {
  name: string;
  stats: Stats;
  steps: ReportStep[] | null;
}

export interface Check {
  source: string;
  scope: string;
  metric: string;
  op: string;
  target: number;
  observed: number;
  pass: boolean;
  targetText: string;
  observedText: string;
}

export interface ErrorRow {
  journey: string;
  step: string;
  error: string;
  count: number;
}

export interface Breakpoint {
  found: boolean;
  lastPass: number;
  firstFail?: number;
  unit: string;
  failedOn?: string[];
}

export interface LoadInfo {
  shape?: string;
  mode: string;
  executor: string;
  peak: number;
  peakVUs: number;
  workers: number;
}

export interface Report {
  formatVersion: number;
  stampede: string;
  runId?: string;
  scenario: string;
  target: string;
  started: string;
  ended: string;
  duration: number;
  stopReason: string;
  load: LoadInfo;
  verdict: Verdict;
  thresholds: Check[] | null;
  overall: Stats;
  journeys: ReportJourney[] | null;
  errors: ErrorRow[] | null;
  timeline: Point[] | null;
  breakpoint?: Breakpoint;
  notes?: string[];
}

export const shapes = [
  'smoke',
  'baseline',
  'stress',
  'spike',
  'soak',
  'breakpoint',
  'steps',
  'recovery',
  'wave',
] as const;
