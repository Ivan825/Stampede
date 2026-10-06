import { clsx } from 'clsx';
import {
  forwardRef,
  useId,
  type ButtonHTMLAttributes,
  type HTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from 'react';
import { AlertTriangle, Loader2 } from 'lucide-react';
import { errorLines } from '@/api/client';

export { clsx as cx };

// ---------------------------------------------------------------- Button

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'danger-solid';
type Size = 'sm' | 'md';

const variants: Record<Variant, string> = {
  primary:
    'bg-primary text-primary-fg hover:bg-primary-hover border border-transparent shadow-sm disabled:hover:bg-primary',
  secondary: 'bg-surface text-fg border border-line hover:bg-surface-2 hover:border-line-strong',
  ghost: 'text-fg hover:bg-surface-2 border border-transparent',
  danger: 'bg-surface text-fail border border-line hover:bg-fail-bg hover:border-fail',
  'danger-solid': 'bg-fail text-danger-fg border border-transparent hover:opacity-90 shadow-sm',
};

const sizes: Record<Size, string> = {
  sm: 'h-7 px-2.5 text-[13px] gap-1.5',
  md: 'h-8 px-3 text-sm gap-2',
};

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'secondary', size = 'md', loading, className, children, disabled, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type="button"
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      className={clsx(
        'inline-flex shrink-0 items-center justify-center rounded-md font-medium whitespace-nowrap transition-colors select-none',
        'disabled:cursor-not-allowed disabled:opacity-50',
        variants[variant],
        sizes[size],
        className,
      )}
      {...rest}
    >
      {loading && <Loader2 className="size-3.5 animate-spin" aria-hidden />}
      {children}
    </button>
  );
});

// ---------------------------------------------------------------- Inputs

const fieldBase =
  'w-full rounded-md border border-line bg-surface px-2.5 text-sm text-fg placeholder:text-muted/70 hover:border-line-strong focus-visible:border-focus disabled:opacity-60 aria-[invalid=true]:border-fail';

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(
  function Input({ className, ...rest }, ref) {
    return <input ref={ref} className={clsx(fieldBase, 'h-8', className)} {...rest} />;
  },
);

export const Textarea = forwardRef<
  HTMLTextAreaElement,
  TextareaHTMLAttributes<HTMLTextAreaElement>
>(function Textarea({ className, ...rest }, ref) {
  return <textarea ref={ref} className={clsx(fieldBase, 'py-1.5', className)} {...rest} />;
});

export const Select = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement>>(
  function Select({ className, children, ...rest }, ref) {
    return (
      <select ref={ref} className={clsx(fieldBase, 'h-8 pr-7', className)} {...rest}>
        {children}
      </select>
    );
  },
);

/** A labelled form field. Passes a generated id to its child via render prop. */
export function Field({
  label,
  hint,
  error,
  children,
  className,
}: {
  label: ReactNode;
  hint?: ReactNode;
  error?: string;
  children: (props: {
    id: string;
    'aria-describedby'?: string;
    'aria-invalid'?: boolean;
  }) => ReactNode;
  className?: string;
}) {
  const id = useId();
  const hintId = hint ? `${id}-hint` : undefined;
  const errId = error ? `${id}-err` : undefined;
  const describedBy = [hintId, errId].filter(Boolean).join(' ') || undefined;
  return (
    <div className={clsx('flex flex-col gap-1', className)}>
      <label htmlFor={id} className="text-[13px] font-medium text-fg">
        {label}
      </label>
      {children({ id, 'aria-describedby': describedBy, 'aria-invalid': error ? true : undefined })}
      {hint && !error && (
        <p id={hintId} className="text-xs text-muted">
          {hint}
        </p>
      )}
      {error && (
        <p id={errId} className="text-xs text-fail">
          {error}
        </p>
      )}
    </div>
  );
}

// ---------------------------------------------------------------- Layout

export function Card({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return <div className={clsx('rounded-lg border border-line bg-surface', className)} {...rest} />;
}

export function CardHeader({
  title,
  actions,
  description,
}: {
  title: ReactNode;
  actions?: ReactNode;
  description?: ReactNode;
}) {
  return (
    <div className="flex items-center gap-3 border-b border-line px-4 py-2.5">
      <div className="min-w-0 flex-1">
        <h2 className="text-sm font-semibold">{title}</h2>
        {description && <p className="text-xs text-muted">{description}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}

export function PageHeader({
  title,
  description,
  actions,
  breadcrumb,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  breadcrumb?: ReactNode;
}) {
  return (
    <div className="mb-5 flex flex-wrap items-end gap-3">
      <div className="min-w-0 flex-1">
        {breadcrumb && <div className="mb-1 text-xs text-muted">{breadcrumb}</div>}
        <h1 className="truncate text-lg font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-0.5 text-[13px] text-muted">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

export function SectionTitle({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mt-7 mb-2.5 flex items-center gap-2">
      <h2 className="label-caps flex-1">{children}</h2>
      {actions}
    </div>
  );
}

// ---------------------------------------------------------------- Table

export function Table({ className, ...rest }: HTMLAttributes<HTMLTableElement>) {
  return (
    <div className="overflow-x-auto">
      <table
        className={clsx(
          'w-full border-collapse text-[13px] [&_td]:border-b [&_td]:border-line [&_td]:px-3 [&_td]:py-2 [&_th]:border-b [&_th]:border-line [&_th]:px-3 [&_th]:py-2 [&_th]:text-left [&_tr:last-child_td]:border-b-0',
          '[&_th]:font-mono [&_th]:text-[11px] [&_th]:font-semibold [&_th]:tracking-[0.08em] [&_th]:text-muted [&_th]:uppercase',
          className,
        )}
        {...rest}
      />
    </div>
  );
}

// ---------------------------------------------------------------- Feedback

export function Spinner({ className, label = 'Loading' }: { className?: string; label?: string }) {
  return (
    <span role="status" className={clsx('inline-flex items-center gap-2 text-muted', className)}>
      <Loader2 className="size-4 animate-spin" aria-hidden />
      <span className="sr-only">{label}</span>
    </span>
  );
}

export function Loading({ label = 'Loading…' }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 px-4 py-10 text-[13px] text-muted" role="status">
      <Loader2 className="size-4 animate-spin" aria-hidden />
      {label}
    </div>
  );
}

/** Shows an API error's message and each of its detail lines. */
export function ErrorAlert({ error, className }: { error: unknown; className?: string }) {
  if (!error) return null;
  const { message, details } = errorLines(error);
  return (
    <div
      role="alert"
      className={clsx(
        'flex gap-2.5 rounded-md border border-fail/40 bg-fail-bg px-3 py-2 text-[13px] text-fg',
        className,
      )}
    >
      <AlertTriangle className="mt-0.5 size-4 shrink-0 text-fail" aria-hidden />
      <div className="min-w-0">
        <p className="font-medium">{message}</p>
        {details.length > 0 && (
          <ul className="mt-1 list-disc space-y-0.5 pl-4 font-mono text-xs text-muted">
            {details.map((d, i) => (
              <li key={i}>{d}</li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

export function Notice({
  tone = 'warn',
  children,
  className,
}: {
  tone?: 'warn' | 'info' | 'pass' | 'fail';
  children: ReactNode;
  className?: string;
}) {
  const tones = {
    warn: 'border-l-warn',
    info: 'border-l-info',
    pass: 'border-l-pass',
    fail: 'border-l-fail',
  };
  return (
    <div
      className={clsx(
        'rounded-md border border-l-[3px] border-line bg-surface px-3 py-2 text-[13px]',
        tones[tone],
        className,
      )}
    >
      {children}
    </div>
  );
}

export function EmptyState({
  title,
  children,
  action,
}: {
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center gap-2 px-6 py-10 text-center">
      <p className="text-sm font-medium">{title}</p>
      {children && <div className="max-w-md text-[13px] text-muted">{children}</div>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="rounded border border-line bg-surface-2 px-1 font-mono text-[11px] text-muted">
      {children}
    </kbd>
  );
}

/** A big number with a caption, for summary rows. */
export function Stat({
  label,
  value,
  sub,
  tone,
}: {
  label: string;
  value: ReactNode;
  sub?: ReactNode;
  tone?: 'fail' | 'warn' | 'pass';
}) {
  return (
    <div className="rounded-lg border border-line bg-surface px-3.5 py-2.5">
      <div
        className={clsx(
          'num text-[22px] leading-tight font-semibold tracking-tight',
          tone === 'fail' && 'text-fail',
          tone === 'warn' && 'text-warn',
          tone === 'pass' && 'text-pass',
        )}
      >
        {value}
      </div>
      <div className="mt-0.5 text-xs text-muted">
        {label}
        {sub && <span> · {sub}</span>}
      </div>
    </div>
  );
}
