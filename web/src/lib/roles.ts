import type { Role } from '@/api/types';

const rank: Record<Role, number> = { viewer: 0, runner: 1, editor: 2, admin: 3, owner: 4 };

export const roles: Role[] = ['owner', 'admin', 'editor', 'runner', 'viewer'];

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

/** What each role can do in the UI. The server enforces the same rules. */
export function permissions(role: Role | undefined) {
  return {
    startRuns: atLeast(role, 'runner'),
    stopRuns: atLeast(role, 'runner'),
    editScenarios: atLeast(role, 'editor'),
    editTargets: atLeast(role, 'editor'),
    editSecrets: atLeast(role, 'editor'),
    editProjects: atLeast(role, 'editor'),
    editSchedules: atLeast(role, 'editor'),
    deleteProjects: atLeast(role, 'admin'),
    manageUsers: atLeast(role, 'admin'),
    viewAudit: atLeast(role, 'admin'),
    manageIntegrations: atLeast(role, 'admin'),
    manageAIProviders: atLeast(role, 'admin'),
    /** Start AI generation jobs and approve their proposals. */
    generateJourneys: atLeast(role, 'editor'),
  };
}

export type Permissions = ReturnType<typeof permissions>;

/** Roles a user of `role` may grant to others or to a token. */
export function grantableRoles(role: Role | undefined): Role[] {
  if (!role) return [];
  return roles.filter((r) => rank[r] <= rank[role]);
}
