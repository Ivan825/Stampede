import { describe, expect, it } from 'vitest';
import { atLeast, grantableRoles, permissions } from './roles';

describe('roles', () => {
  it('ranks roles', () => {
    expect(atLeast('owner', 'admin')).toBe(true);
    expect(atLeast('runner', 'editor')).toBe(false);
    expect(atLeast(undefined, 'viewer')).toBe(false);
  });

  it('lets runners start runs but not edit scenarios', () => {
    const p = permissions('runner');
    expect(p.startRuns).toBe(true);
    expect(p.editScenarios).toBe(false);
    expect(p.manageUsers).toBe(false);
    expect(p.generateJourneys).toBe(false);
  });

  it('lets editors generate journeys and admins manage AI providers', () => {
    expect(permissions('editor').generateJourneys).toBe(true);
    expect(permissions('editor').manageAIProviders).toBe(false);
    expect(permissions('admin').manageAIProviders).toBe(true);
  });

  it('gives viewers read-only access', () => {
    expect(Object.values(permissions('viewer')).every((v) => !v)).toBe(true);
  });

  it('only grants roles up to your own', () => {
    expect(grantableRoles('admin')).toEqual(['admin', 'editor', 'runner', 'viewer']);
    expect(grantableRoles('owner')[0]).toBe('owner');
  });
});
