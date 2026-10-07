import type { Role } from '@/api/types';

const rank: Record<Role, number> = { viewer: 0, runner: 1, editor: 2, admin: 3, owner: 4 };

export const roleDescriptions: Record<Role, string> = {
  owner: 'Everything, including the organisation itself',
  admin: 'Manage users, tokens and every project',
  editor: 'Edit scenarios, targets and secrets, and start runs',
  runner: 'Start and stop runs',
  viewer: 'Read only',
};

/** True when `role` is at least `min`. */
export function atLeast(role: Role | undefined, min: Role): boolean {
  if (!role) return false;
  return rank[role] >= rank[min];
}

/**
 * What each role can see or do in the read-only UI. Changes are made with
 * the CLI; the server enforces the same rules.
 */
export function permissions(role: Role | undefined) {
  return {
    /** The safety controls: stop or kill a run, and the kill switch. */
    stopRuns: atLeast(role, 'runner'),
    /** Have the server fetch an API spec from a target's host for a coverage check. */
    fetchSpecs: atLeast(role, 'editor'),
    /** See the SSO and limits settings. */
    manageUsers: atLeast(role, 'admin'),
    viewAudit: atLeast(role, 'admin'),
    manageIntegrations: atLeast(role, 'admin'),
    manageAIProviders: atLeast(role, 'admin'),
  };
}

export type Permissions = ReturnType<typeof permissions>;
