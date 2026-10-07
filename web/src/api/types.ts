import type { components } from './schema.gen';

type S = components['schemas'];

export type ApiErrorBody = S['Error'];
export type VersionInfo = S['VersionInfo'];
export type Role = S['Role'];
export type SetupRequest = S['SetupRequest'];
export type LoginRequest = S['LoginRequest'];
export type Session = S['Session'];
export type Me = S['Me'];
export type AuthConfig = S['AuthConfig'];
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
export type Narrative = S['Narrative'];
export type NarrativeClaim = S['NarrativeClaim'];
export type NarrativeFact = S['NarrativeFact'];
export type NarrativeResult = S['NarrativeResult'];
export type RunOverrides = S['RunOverrides'];
export type RunCreate = S['RunCreate'];
export type Run = S['Run'];
export type RunSummary = S['RunSummary'];
export type Schedule = S['Schedule'];
export type ScheduleCreate = S['ScheduleCreate'];
export type ScheduleUpdate = S['ScheduleUpdate'];
export type SchedulePreview = S['SchedulePreview'];
export type Point = S['Point'];
export type Worker = S['Worker'];
export type AuditEntry = S['AuditEntry'];
export type Integration = S['Integration'];
export type IntegrationKind = S['IntegrationKind'];
export type IntegrationCreate = S['IntegrationCreate'];
export type NotificationKind = S['NotificationKind'];
export type NotificationEvent = S['NotificationEvent'];
export type NotificationChannel = S['NotificationChannel'];
export type NotificationChannelCreate = S['NotificationChannelCreate'];
export type NotificationChannelCreated = S['NotificationChannelCreated'];
export type NotificationDelivery = S['NotificationDelivery'];
export type AIProviderKind = S['AIProviderKind'];
export type AIProvider = S['AIProvider'];
export type AIProviderPut = S['AIProviderPut'];
export type AIJobCreate = S['AIJobCreate'];
export type AIJobStatus = S['AIJobStatus'];
export type AIJobSummary = S['AIJobSummary'];
export type AIJob = S['AIJob'];
export type AIJourney = S['AIJourney'];
export type AITrace = S['AITrace'];
export type AIStepTrace = S['AIStepTrace'];
export type AICheck = S['AICheck'];
export type AIProblem = S['AIProblem'];
export type AIUsage = S['AIUsage'];
export type AIJobApprove = S['AIJobApprove'];
export type AIJobApproval = S['AIJobApproval'];
export type CompareRequest = S['CompareRequest'];
export type Comparison = S['Comparison'];
export type CompareVerdict = S['CompareVerdict'];
export type MetricDelta = S['MetricDelta'];
export type StepDelta = S['StepDelta'];

export const aiProviderKinds: readonly AIProviderKind[] = [
  'anthropic',
  'openai',
  'gemini',
  'ollama',
  'openai-compatible',
];

export function isAIJobActive(status: AIJobStatus): boolean {
  return status === 'queued' || status === 'running';
}

/** Runs that finished with a report, so they can be compared. */
export function hasReport(run: Run): boolean {
  return (run.status === 'completed' || run.status === 'aborted') && run.startedAt != null;
}

export const notificationEvents: readonly NotificationEvent[] = [
  'run.finished',
  'run.target_failed',
  'run.killed',
];

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

/** One of a step's slowest requests, with the trace it was sent in. */
export interface SlowRequest {
  /** Seconds, measured from the intended send time. */
  latency: number;
  at: string;
  /** Seconds since the run started. */
  t: number;
  traceId?: string;
  traceUrl?: string;
  status?: number;
  error?: string;
}

export interface ReportStep {
  id: number;
  name: string;
  stats: Stats;
  phases: Record<string, PhaseStat>;
  slowest?: SlowRequest[];
  /** Browser page timings and Web Vitals (seconds; cls unitless). */
  browser?: BrowserStat;
}

export interface BrowserStat {
  ttfb?: PhaseStat;
  fcp?: PhaseStat;
  lcp?: PhaseStat;
  cls?: PhaseStat;
  inp?: PhaseStat;
  load?: PhaseStat;
}

/** One Prometheus query evaluated over the run (observe.prometheus). */
export interface TargetMetric {
  name: string;
  query: string;
  points: { t: number; value: number }[];
  error?: string;
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
  /** Up to three of these failures, with what was sent and received (secrets redacted). */
  examples?: ErrorExample[];
}

/** One failed request as sent and received (internal/metrics ErrorExample). */
export interface ErrorExample {
  at: string;
  traceId?: string;
  /** "METHOD URL". */
  request: string;
  requestHeaders?: Record<string, string>;
  requestBody?: string;
  status?: number;
  responseHeaders?: Record<string, string>;
  responseBody?: string;
  /** The error message, such as a check's mismatch or a connection error. */
  detail?: string;
}

/** One confirmation hold of a breakpoint search. */
export interface RefineStep {
  level: number;
  pass: boolean;
  failedOn?: string[];
}

export interface Breakpoint {
  found: boolean;
  lastPass: number;
  firstFail?: number;
  unit: string;
  failedOn?: string[];
  /** Holds that narrowed lastPass and firstFail after the first failing step. */
  refined?: RefineStep[];
}

/** One load level of a run whose load changed over time. */
export interface CurvePoint {
  /** Planned load: users, or iterations per second. */
  offered: number;
  /** Completed iterations per second. */
  throughput: number;
  rps: number;
  p50: number;
  p95: number;
  p99: number;
  errorRate: number;
  seconds: number;
}

/** Where adding load stops adding throughput. */
export interface Knee {
  found: boolean;
  /** The last level that still scaled. */
  at: CurvePoint;
  /** The first level that did not. */
  next?: CurvePoint;
  unit: string;
  reason?: string;
}

/** How long the target took to return to normal after a spike or overload. */
export interface Recovery {
  recovered: boolean;
  seconds?: number;
  /** When load returned to normal, seconds since start. */
  normalAt: number;
  baselineP95: number;
  baselineErrorRate: number;
}

/** A time window in seconds since the start; `to` is exclusive. */
export interface Span {
  from: number;
  to: number;
}

/** One worker's part in a run (internal/report/workers.go). */
export interface WorkerRow {
  id: string;
  name: string;
  region?: string;
  shareLo: number;
  shareHi: number;
  state: string;
  stopReason?: string;
  error?: string;
  peakVUs: number;
  requests: number;
  saturated?: Span[];
  saturationReasons?: string[];
  lost?: Span;
  replaces?: string;
  /** Measured clock offset in seconds. */
  clockOffset: number;
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
  /** Throughput against load, for runs whose load changed over time. */
  curve?: CurvePoint[];
  knee?: Knee;
  /** Set for spike and recovery shapes. */
  recovery?: Recovery;
  notes?: string[];
  /** Each worker of a distributed run; an in-process run lists its machine only when saturated. */
  workers?: WorkerRow[];
  targetMetrics?: TargetMetric[];
  /** AI-written summary; every claim cites report figures. */
  narrative?: Narrative;
  /** Faults a stampede agent injected during the run. */
  faults?: FaultEvent[];
}

/** A fault injected during the run; start and end are seconds since it started. */
export interface FaultEvent {
  label: string;
  kind: 'proxy' | 'container' | 'deployment';
  target: string;
  start: number;
  end: number;
  error?: string;
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
