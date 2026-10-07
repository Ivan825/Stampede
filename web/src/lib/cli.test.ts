import { describe, expect, it } from 'vitest';
import { cli, shellArg } from './cli';

describe('cli commands', () => {
  it('quotes arguments only when the shell needs it', () => {
    expect(shellArg('shop-smoke')).toBe('shop-smoke');
    expect(shellArg('<name>')).toBe('<name>');
    expect(shellArg('nightly run')).toBe(`'nightly run'`);
    expect(shellArg(`it's`)).toBe(`'it'\\''s'`);
  });

  it('fills in what the page knows', () => {
    expect(cli.start()).toBe('stampede start --scenario <scenario> --target <target>');
    expect(cli.start({ project: 'shop', scenario: 'checkout', target: 'staging' })).toBe(
      'stampede start --project shop --scenario checkout --target staging',
    );
    expect(cli.schedulesRun('nightly checkout')).toBe(`stampede schedules run 'nightly checkout'`);
    expect(cli.push('shop')).toBe('stampede push --project shop <scenario.yaml>');
    expect(cli.packInstall('shop')).toBe('stampede pack install shop');
  });
});
