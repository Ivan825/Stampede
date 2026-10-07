import { describe, expect, it } from 'vitest';
import { atLeast, permissions } from './roles';

describe('roles', () => {
  it('ranks roles', () => {
    expect(atLeast('owner', 'admin')).toBe(true);
    expect(atLeast('runner', 'editor')).toBe(false);
    expect(atLeast(undefined, 'viewer')).toBe(false);
  });

  it('lets runners use the safety controls but not see admin settings', () => {
    const p = permissions('runner');
    expect(p.stopRuns).toBe(true);
    expect(p.fetchSpecs).toBe(false);
    expect(p.manageUsers).toBe(false);
  });

  it('shows admin settings to admins only', () => {
    expect(permissions('editor').manageAIProviders).toBe(false);
    expect(permissions('admin').manageAIProviders).toBe(true);
    expect(permissions('admin').viewAudit).toBe(true);
  });

  it('gives viewers nothing beyond reading', () => {
    expect(Object.values(permissions('viewer')).every((v) => !v)).toBe(true);
  });
});
