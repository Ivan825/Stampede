import type { Target } from '@/api/types';
import { Chip } from '@/components/chips';

export function TargetBadge({ target }: { target: Target }) {
  if (target.private) return <Chip tone="info">private</Chip>;
  if (target.verified) return <Chip tone="pass">verified</Chip>;
  return <Chip tone="warn">unverified</Chip>;
}
