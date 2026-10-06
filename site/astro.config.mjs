import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  site: 'https://stampede.vercel.app',
  integrations: [
    starlight({
      title: 'Stampede docs',
      logo: { src: './src/assets/bull.svg', alt: 'Stampede' },
      favicon: '/favicon.svg',
      social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/Ivan825/Stampede' }],
      editLink: { baseUrl: 'https://github.com/Ivan825/Stampede/edit/main/' },
      customCss: ['./src/styles/starlight.css'],
      head: [
        { tag: 'link', attrs: { rel: 'preconnect', href: 'https://fonts.googleapis.com' } },
        { tag: 'link', attrs: { rel: 'preconnect', href: 'https://fonts.gstatic.com', crossorigin: true } },
        { tag: 'link', attrs: { rel: 'stylesheet', href: 'https://fonts.googleapis.com/css2?family=Archivo:wdth,wght@62..125,400..800&family=IBM+Plex+Sans:ital,wght@0,400;0,500;0,600;1,400&family=JetBrains+Mono:wght@400;600&display=swap' } },
      ],
      sidebar: [
        { label: 'Start', items: ['docs/quickstart', 'docs/install', 'docs/guides/first-target'] },
        { label: 'Concepts', items: [{ autogenerate: { directory: 'docs/concepts' } }] },
        { label: 'Guides', items: ['docs/guides/test-types', 'docs/guides/reports', 'docs/guides/comparing', 'docs/guides/ci', 'docs/guides/schedules', 'docs/guides/integrations', 'docs/guides/faults', 'docs/guides/coverage', 'docs/ai', 'docs/guides/packs', 'docs/protocols', 'docs/plugins'] },
        { label: 'Operate', items: ['docs/deploy/compose', 'docs/deploy/helm', 'docs/deploy/operator', 'docs/deploy/terraform', 'docs/deploy/airgap', 'docs/deploy/upgrades', 'docs/safety', 'docs/threat-model', 'docs/reference/configuration'] },
        { label: 'Reference', items: ['docs/reference/scenario', { label: 'CLI', collapsed: true, items: [{ autogenerate: { directory: 'docs/reference/cli' } }] }] },
        { label: 'Project', items: ['docs/architecture', 'docs/troubleshooting'] },
      ],
    }),
  ],
});
