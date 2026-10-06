import fs from 'node:fs';
import path from 'node:path';
import yaml from 'js-yaml';

const repo = path.resolve(process.cwd(), '..');

export type Feature = { name: string; status: 'shipped' | 'planned'; doc?: string };
export type Group = { name: string; features: Feature[] };
export type Pack = { name: string; title: string; status: 'shipped' | 'planned'; signature: string; drivers: string[] };

/** features.yaml: what works today and what is planned. */
export function features(): Group[] {
  return (yaml.load(fs.readFileSync(path.join(repo, 'features.yaml'), 'utf8')) as { groups: Group[] }).groups;
}

/** packs/catalog.yaml: every product type and whether its pack ships. */
export function packs(): Pack[] {
  return (yaml.load(fs.readFileSync(path.join(repo, 'packs', 'catalog.yaml'), 'utf8')) as { packs: Pack[] }).packs;
}

/** Files in a shipped pack, for its page. */
export function packFiles(name: string): { dir: string; files: string[] }[] {
  const root = path.join(repo, 'packs', name);
  return ['journeys', 'stresses'].map((dir) => ({
    dir,
    files: fs.existsSync(path.join(root, dir)) ? fs.readdirSync(path.join(root, dir)).filter((f) => f.endsWith('.yaml')) : [],
  }));
}

export function packReadme(name: string): string {
  const p = path.join(repo, 'packs', name, 'README.md');
  return fs.existsSync(p) ? fs.readFileSync(p, 'utf8') : '';
}

export function isShipped(name: string): boolean {
  return features().some((g) => g.features.some((f) => f.name === name && f.status === 'shipped'));
}

export const GITHUB = 'https://github.com/Ivan825/Stampede';
