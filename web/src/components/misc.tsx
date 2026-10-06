import { Check, Copy, Monitor, Moon, Sun } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Button } from './ui';
import * as Tooltip from '@radix-ui/react-tooltip';
import type { ReactNode } from 'react';

/** The Stampede mark: four ink bars and one accent bar. */
export function Mark({ className = 'size-6' }: { className?: string }) {
  return (
    <svg viewBox="0 0 68 64" className={className} role="img" aria-label="Stampede">
      <g fill="currentColor">
        <polygon points="1,57 8.5,57 14.98,39 7.48,39" />
        <polygon points="11,57 18.5,57 27.86,31 20.36,31" />
        <polygon points="21,57 28.5,57 40.74,23 33.24,23" />
        <polygon points="31,57 38.5,57 53.62,15 46.12,15" />
      </g>
      <polygon points="41,57 48.5,57 66.5,7 59,7" fill="var(--accent)" />
    </svg>
  );
}

export function CopyButton({ value, label = 'Copy' }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(t);
  }, [copied]);
  return (
    <Button
      size="sm"
      onClick={() => {
        void navigator.clipboard?.writeText(value).then(() => setCopied(true));
      }}
      aria-label={copied ? 'Copied' : label}
    >
      {copied ? (
        <Check className="size-3.5 text-pass" aria-hidden />
      ) : (
        <Copy className="size-3.5" aria-hidden />
      )}
      {copied ? 'Copied' : label}
    </Button>
  );
}

export function Tip({ content, children }: { content: ReactNode; children: ReactNode }) {
  return (
    <Tooltip.Root delayDuration={300}>
      <Tooltip.Trigger asChild>{children}</Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content
          sideOffset={6}
          className="z-[70] max-w-xs rounded-md border border-line bg-surface px-2 py-1 text-xs text-fg shadow-md"
        >
          {content}
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}

// ---------------------------------------------------------------- Theme

export type ThemePref = 'system' | 'light' | 'dark';
const THEME_KEY = 'stampede.theme';

function readPref(): ThemePref {
  try {
    const v = localStorage.getItem(THEME_KEY);
    return v === 'light' || v === 'dark' ? v : 'system';
  } catch {
    return 'system';
  }
}

function applyPref(p: ThemePref) {
  const root = document.documentElement;
  if (p === 'system') delete root.dataset.theme;
  else root.dataset.theme = p;
  try {
    if (p === 'system') localStorage.removeItem(THEME_KEY);
    else localStorage.setItem(THEME_KEY, p);
  } catch {
    /* storage unavailable */
  }
  window.dispatchEvent(new Event('stampede-theme'));
}

/** Whether the effective theme is dark, updating on OS or user changes. */
export function useIsDark(): boolean {
  const compute = () => {
    const t = document.documentElement.dataset.theme;
    if (t === 'dark') return true;
    if (t === 'light') return false;
    return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false;
  };
  const [dark, setDark] = useState(compute);
  useEffect(() => {
    const mq = window.matchMedia?.('(prefers-color-scheme: dark)');
    const update = () => setDark(compute());
    mq?.addEventListener('change', update);
    window.addEventListener('stampede-theme', update);
    return () => {
      mq?.removeEventListener('change', update);
      window.removeEventListener('stampede-theme', update);
    };
  }, []);
  return dark;
}

export function ThemeToggle() {
  const [pref, setPref] = useState<ThemePref>(readPref);
  const next: Record<ThemePref, ThemePref> = { system: 'light', light: 'dark', dark: 'system' };
  const Icon = pref === 'system' ? Monitor : pref === 'light' ? Sun : Moon;
  const label = `Theme: ${pref}. Switch to ${next[pref]}.`;
  return (
    <Tip content={label}>
      <Button
        variant="ghost"
        size="sm"
        aria-label={label}
        className="px-1.5"
        onClick={() => {
          const n = next[pref];
          setPref(n);
          applyPref(n);
        }}
      >
        <Icon className="size-4" aria-hidden />
      </Button>
    </Tip>
  );
}

/** Reads a CSS custom property from the root element (for chart colours). */
export function cssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}
