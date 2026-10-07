import type { LimitCaps } from '@/api/types';
import { humanDuration, num } from '@/lib/format';

/** "≤ 100/s · ≤ 500 VUs · ≤ 1h", or "No caps". */
export function capsText(c: LimitCaps | undefined): string {
  const parts = [
    c?.maxRate != null && `≤ ${num(c.maxRate)}/s`,
    c?.maxVUs != null && `≤ ${c.maxVUs.toLocaleString('en-US')} VUs`,
    c?.maxDurationSeconds != null && `≤ ${humanDuration(c.maxDurationSeconds)}`,
  ].filter(Boolean);
  return parts.length ? parts.join(' · ') : 'No caps';
}
