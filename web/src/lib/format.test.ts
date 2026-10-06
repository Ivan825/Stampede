import { describe, expect, it } from 'vitest';
import {
  bytes,
  clock,
  compareValue,
  count,
  humanDuration,
  load,
  ms,
  num,
  pct,
  rate,
  relativeTime,
  secs,
  signedPct,
} from './format';

describe('ms (matches report.Ms in Go)', () => {
  it.each([
    [0, '0.00ms'],
    [0.0042, '4.20ms'],
    [0.01, '10.0ms'],
    [0.1234, '123.4ms'],
    [0.999, '999.0ms'],
    [1, '1000ms'],
    [2.5, '2500ms'],
    [9.9996, '10000ms'],
    [10, '10.0s'],
    [61.25, '61.3s'],
  ])('%s s -> %s', (input, want) => {
    expect(ms(input)).toBe(want);
  });

  it('shows a dash for missing values', () => {
    expect(ms(undefined)).toBe('–');
    expect(ms(null)).toBe('–');
    expect(ms(Number.NaN)).toBe('–');
  });
});

describe('pct, bytes, num', () => {
  it('formats fractions as percentages with two decimals', () => {
    expect(pct(0)).toBe('0.00%');
    expect(pct(0.0123)).toBe('1.23%');
    expect(pct(1)).toBe('100.00%');
    expect(pct(0.5, 0)).toBe('50%');
  });

  it('formats bytes with binary units', () => {
    expect(bytes(512)).toBe('512 B');
    expect(bytes(1536)).toBe('1.5 KiB');
    expect(bytes(5 * 1024 * 1024)).toBe('5.00 MiB');
    expect(bytes(3 * 1024 ** 3)).toBe('3.00 GiB');
  });

  it('keeps whole numbers whole', () => {
    expect(num(50)).toBe('50');
    expect(num(12.345)).toBe('12.3');
  });

  it('formats load levels with the unit for the mode', () => {
    expect(load(50, 'rate')).toBe('50/s');
    expect(load(20, 'vus')).toBe('20 VUs');
  });
});

describe('rates, counts and durations', () => {
  it('rate keeps one decimal below 100', () => {
    expect(rate(48.04)).toBe('48.0');
    expect(rate(1234.6)).toBe('1,235');
  });

  it('count groups thousands', () => {
    expect(count(1234567)).toBe('1,234,567');
  });

  it('secs matches fmtSecs in Go', () => {
    expect(secs(42)).toBe('42s');
    expect(secs(119)).toBe('119s');
    expect(secs(300)).toBe('5m');
    expect(secs(5400)).toBe('1.5h');
  });

  it('clock pads minutes and seconds', () => {
    expect(clock(0)).toBe('0:00');
    expect(clock(75)).toBe('1:15');
    expect(clock(3725)).toBe('1:02:05');
  });

  it('humanDuration drops seconds above an hour', () => {
    expect(humanDuration(45)).toBe('45s');
    expect(humanDuration(90)).toBe('1m 30s');
    expect(humanDuration(7260)).toBe('2h 1m');
  });

  it('relativeTime is relative to now', () => {
    const now = Date.parse('2026-10-06T12:00:00Z');
    expect(relativeTime('2026-10-06T11:59:50Z', now)).toBe('just now');
    expect(relativeTime('2026-10-06T11:55:00Z', now)).toBe('5 minutes ago');
    expect(relativeTime('2026-10-06T09:00:00Z', now)).toBe('3 hours ago');
    expect(relativeTime('2026-10-05T12:00:00Z', now)).toBe('yesterday');
    expect(relativeTime(null, now)).toBe('–');
  });

  it('signedPct matches report.signed in Go', () => {
    expect(signedPct(0.1234)).toBe('+12.3%');
    expect(signedPct(-0.05)).toBe('-5.0%');
    expect(signedPct(0)).toBe('+0.0%');
    expect(signedPct(-0.0001)).toBe('-0.0%');
    expect(signedPct(null)).toBe('+∞');
  });

  it('compareValue formats each compared metric in its unit', () => {
    expect(compareValue('p95', 0.1234)).toBe('123.4ms');
    expect(compareValue('error rate', 0.0123)).toBe('1.23%');
    expect(compareValue('throughput', 99.95)).toBe('100.0/s');
    expect(compareValue('max sustainable load', 250)).toBe('250');
  });
});
