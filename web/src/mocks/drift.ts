/**
 * Scheduled drift checks for the mock API: a drift schedule's results, and
 * the check a "Check now" runs. Each check dry-runs the scenario's journeys
 * once against the target and compares it with the schedule's spec.
 */
import type { DriftJourney, DriftResult, Schedule, Scenario, Target } from '@/api/types';
import { step } from './ai';
import { analyse } from './sim';

/** The journey the mock API's latest release broke. */
export const brokenJourney = 'login';

function journeysOf(s: Scenario, base: string, drifted: boolean): DriftJourney[] {
  return analyse(s.latestVersion.yaml).journeys.map(({ name }) => {
    if (drifted && name === brokenJourney) {
      return {
        journey: name,
        ok: false,
        problem: 'step 1 (POST /api/login): status 200 expected, got 404',
        traces: [
          {
            pass: 1,
            ok: false,
            error: 'step 1 (POST /api/login) failed',
            steps: [
              step('POST /api/login', 'POST', `${base}/api/login`, 404, 12.4, {
                requestHeaders: { 'Content-Type': 'application/json' },
                requestBody: '{"email":"[redacted email]","password":"[redacted]"}',
                responseBody: '{"error":"not found"}',
                checks: [{ name: 'status 200', ok: false, detail: 'got 404' }],
                error: 'check failed: status 200 expected, got 404',
              }),
            ],
          },
        ],
      };
    }
    return {
      journey: name,
      ok: true,
      traces: [
        {
          pass: 1,
          ok: true,
          steps: [
            step('GET /api/products', 'GET', `${base}/api/products?page=3`, 200, 36.1, {
              responseBody: '{"items":[{"id":12,"name":"Desk lamp"}],"page":3}',
              extracted: { productId: '12' },
            }),
          ],
        },
      ],
    };
  });
}

/** One check's result; `drifted` decides whether a login journey, if any, broke. */
export function driftCheck(args: {
  id: string;
  schedule: Schedule;
  scenario: Scenario;
  target: Target;
  drifted: boolean;
  at: string;
}): DriftResult {
  const { schedule, scenario, target, drifted } = args;
  const journeys = journeysOf(scenario, target.baseURL, drifted);
  const broken = journeys.filter((j) => !j.ok).map((j) => j.journey);
  const spec = !!schedule.specURL;
  return {
    id: args.id,
    projectId: schedule.projectId,
    scheduleId: schedule.id,
    scheduleName: schedule.name,
    scenarioId: scenario.id,
    scenarioName: scenario.name,
    scenarioVersion: scenario.latestVersion.version,
    targetId: target.id,
    targetURL: target.baseURL,
    status: broken.length ? 'drifted' : 'ok',
    broken,
    journeys,
    ...(broken.length && spec
      ? {
          removedEndpoints: ['POST /api/login'],
          addedEndpoints: ['POST /api/v2/sessions'],
          unmatched: ['login: POST /api/login'],
        }
      : {}),
    repairJobId: null,
    createdAt: args.at,
  };
}

/** A result as GET /projects/{id}/drift-results lists it: without traces. */
export function driftSummary(r: DriftResult): DriftResult {
  return {
    ...r,
    journeys: (r.journeys ?? []).map(({ traces: _t, ...j }) => j),
  };
}
