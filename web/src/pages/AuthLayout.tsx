import type { ReactNode } from 'react';
import { Mark, ThemeToggle } from '@/components/misc';
import { useVersion } from '@/api/queries';

export function AuthLayout({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle?: ReactNode;
  children: ReactNode;
}) {
  const version = useVersion();
  return (
    <div className="flex min-h-full flex-col items-center justify-center bg-bg px-4 py-12">
      <div className="absolute top-3 right-3">
        <ThemeToggle />
      </div>
      <div className="w-full max-w-sm">
        <div className="mb-6 flex items-center gap-2.5">
          <Mark className="h-7 w-auto" />
          <span className="text-lg font-semibold tracking-tight">Stampede</span>
        </div>
        <main className="rounded-lg border border-line bg-surface p-6 shadow-sm">
          <h1 className="text-base font-semibold">{title}</h1>
          {subtitle && <p className="mt-1 text-[13px] text-muted">{subtitle}</p>}
          <div className="mt-5">{children}</div>
        </main>
        {version.data && (
          <p className="num mt-4 text-center text-xs text-muted">Stampede {version.data.version}</p>
        )}
      </div>
    </div>
  );
}
