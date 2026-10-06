// Copies ../docs into the Starlight content folder so the docs are written
// once, read on GitHub and served on the website. It adds the frontmatter
// Starlight needs (the title comes from the first heading) and rewrites
// links: other docs become site routes, files elsewhere in the repository
// become GitHub links.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, '..', '..');
const src = path.join(repo, 'docs');
const out = path.join(here, '..', 'src', 'content', 'docs', 'docs');
const GH = 'https://github.com/Ivan825/Stampede/blob/main/';

fs.rmSync(out, { recursive: true, force: true });

function walk(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    return e.isDirectory() ? walk(p) : e.name.endsWith('.md') ? [p] : [];
  });
}

function route(file) {
  let rel = path.relative(src, file).replace(/\\/g, '/').replace(/\.md$/, '');
  rel = rel.replace(/(^|\/)README$/, '$1index');
  return '/docs/' + rel.replace(/(^|\/)index$/, '$1').replace(/\/$/, '') + '/';
}

let count = 0;
for (const file of walk(src)) {
  let text = fs.readFileSync(file, 'utf8');
  const m = text.match(/^#{1,2} (.+)$/m);
  const title = m ? m[1].trim() : path.basename(file, '.md');
  if (m) text = text.replace(m[0], '');
  text = text.replace(/\]\(([^)\s]+)\)/g, (all, target) => {
    if (/^(https?:|mailto:|#)/.test(target)) return all;
    const [p, hash = ''] = target.split('#');
    const abs = path.resolve(path.dirname(file), p);
    const anchor = hash ? '#' + hash : '';
    if (abs.startsWith(src) && abs.endsWith('.md') && fs.existsSync(abs)) return `](${route(abs)}${anchor})`;
    if (abs.startsWith(repo)) return `](${GH}${path.relative(repo, abs).replace(/\\/g, '/')}${anchor})`;
    return all;
  });
  let rel = path.relative(src, file).replace(/(^|\/)README\.md$/, '$1index.md');
  const dest = path.join(out, rel);
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  const fm = `---\ntitle: ${JSON.stringify(title)}\neditUrl: ${GH.replace('/blob/', '/edit/')}docs/${path.relative(src, file).replace(/\\/g, '/')}\n---\n`;
  fs.writeFileSync(dest, fm + text.trimStart());
  count++;
}
console.log(`synced ${count} docs pages`);
