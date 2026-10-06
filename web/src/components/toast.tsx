import { clsx } from 'clsx';
import { CheckCircle2, AlertTriangle, X } from 'lucide-react';
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';
import { errorLines } from '@/api/client';

interface Toast {
  id: number;
  tone: 'success' | 'error';
  message: string;
  details?: string[];
}

interface ToastApi {
  success: (message: string) => void;
  error: (err: unknown) => void;
}

const ToastContext = createContext<ToastApi | null>(null);

let nextId = 1;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);

  const dismiss = useCallback((id: number) => {
    setToasts((t) => t.filter((x) => x.id !== id));
  }, []);

  const push = useCallback(
    (t: Omit<Toast, 'id'>) => {
      const id = nextId++;
      setToasts((list) => [...list.slice(-3), { ...t, id }]);
      setTimeout(() => dismiss(id), t.tone === 'error' ? 8000 : 4000);
    },
    [dismiss],
  );

  const api = useMemo<ToastApi>(
    () => ({
      success: (message) => push({ tone: 'success', message }),
      error: (err) => {
        const { message, details } = errorLines(err);
        push({ tone: 'error', message, details });
      },
    }),
    [push],
  );

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div
        aria-live="polite"
        className="pointer-events-none fixed right-4 bottom-4 z-[60] flex w-80 flex-col gap-2"
      >
        {toasts.map((t) => (
          <div
            key={t.id}
            role={t.tone === 'error' ? 'alert' : 'status'}
            className={clsx(
              'pointer-events-auto flex gap-2.5 rounded-md border bg-surface px-3 py-2.5 text-[13px] shadow-lg',
              t.tone === 'error' ? 'border-fail/50' : 'border-line',
            )}
          >
            {t.tone === 'error' ? (
              <AlertTriangle className="mt-0.5 size-4 shrink-0 text-fail" aria-hidden />
            ) : (
              <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-pass" aria-hidden />
            )}
            <div className="min-w-0 flex-1">
              <p>{t.message}</p>
              {t.details?.map((d, i) => (
                <p key={i} className="font-mono text-xs text-muted">
                  {d}
                </p>
              ))}
            </div>
            <button
              type="button"
              onClick={() => dismiss(t.id)}
              className="-mr-1 rounded p-0.5 text-muted hover:text-fg"
              aria-label="Dismiss"
            >
              <X className="size-3.5" aria-hidden />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error('useToast outside ToastProvider');
  return ctx;
}
