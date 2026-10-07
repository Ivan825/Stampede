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

/** Whether the feature whose name starts with prefix is shipped; a missing feature is not. */
export function isShipped(prefix: string): boolean {
  return features().some((g) => g.features.some((f) => f.name.startsWith(prefix) && f.status === 'shipped'));
}

/** The status of the feature whose name starts with prefix. */
export function statusOf(prefix: string): 'shipped' | 'planned' {
  return isShipped(prefix) ? 'shipped' : 'planned';
}

export const GITHUB = 'https://github.com/Ivan825/Stampede';

const escape = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

/** Inline Markdown: `code`, **bold** and [links]; repository paths link to GitHub. */
function inline(s: string): string {
  return escape(s)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_, text: string, href: string) => {
      const url = /^(https?:|mailto:|#|\/)/.test(href) ? href : `${GITHUB}/blob/main/${href.replace(/^\.\//, '')}`;
      return `<a href="${url}">${text}</a>`;
    });
}

/**
 * CHANGELOG.md as HTML, without its title, or '' when there is none. It
 * understands the subset the changelog uses: ## and ### headings,
 * paragraphs and "- " lists whose items may wrap onto indented lines.
 */
export function changelogHTML(): string {
  const p = path.join(repo, 'CHANGELOG.md');
  if (!fs.existsSync(p)) return '';
  const out: string[] = [];
  let para: string[] = [];
  let items: string[] | null = null;
  const flush = () => {
    if (para.length) out.push(`<p>${inline(para.join(' '))}</p>`);
    if (items) out.push(`<ul>${items.map((i) => `<li>${inline(i)}</li>`).join('')}</ul>`);
    para = [];
    items = null;
  };
  for (const raw of fs.readFileSync(p, 'utf8').split('\n')) {
    const line = raw.trimEnd();
    const h = line.match(/^(#{1,6}) (.+)$/);
    if (h) {
      flush();
      // The page has its own title; the file's # heading is dropped.
      if (h[1].length > 1) out.push(`<h${h[1].length}>${inline(h[2])}</h${h[1].length}>`);
    } else if (line.startsWith('- ')) {
      if (para.length) flush();
      (items ??= []).push(line.slice(2));
    } else if (items && /^\s+\S/.test(line)) {
      items[items.length - 1] += ' ' + line.trim();
    } else if (line === '') {
      flush();
    } else {
      if (items) flush();
      para.push(line);
    }
  }
  flush();
  return out.join('\n');
}
