import { useEffect, useState } from 'react';
import { validateScenario } from '@/api/queries';
import type { Validation } from '@/api/types';

interface Checked {
  yaml: string;
  result: Validation | null;
  error: unknown;
}

/**
 * Validates YAML on the server as the user types, debounced. `pending` is
 * true while the latest text has not been checked yet.
 */
export function useValidation(yaml: string, delayMs = 450) {
  const [checked, setChecked] = useState<Checked | null>(null);

  useEffect(() => {
    if (!yaml.trim()) return;
    const ctrl = new AbortController();
    const timer = setTimeout(() => {
      validateScenario(yaml, ctrl.signal)
        .then((result) => setChecked({ yaml, result, error: null }))
        .catch((error: unknown) => {
          if (!ctrl.signal.aborted) setChecked({ yaml, result: null, error });
        });
    }, delayMs);
    return () => {
      clearTimeout(timer);
      ctrl.abort();
    };
  }, [yaml, delayMs]);

  const empty = !yaml.trim();
  return {
    // Keep showing the previous result while the new text is checked.
    result: empty ? null : (checked?.result ?? null),
    error: empty ? null : (checked?.error ?? null),
    pending: !empty && checked?.yaml !== yaml,
  };
}
