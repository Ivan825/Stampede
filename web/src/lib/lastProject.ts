const KEY = 'stampede.project';

/** Remembers the last project the user opened, per browser. */
export const lastProject = {
  get(): string | null {
    try {
      return localStorage.getItem(KEY);
    } catch {
      return null;
    }
  },
  set(id: string) {
    try {
      localStorage.setItem(KEY, id);
    } catch {
      /* storage unavailable */
    }
  },
};
