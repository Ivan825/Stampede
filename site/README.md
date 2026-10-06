# Stampede website

Astro with Starlight for the docs. Feature and pack labels come from
`../features.yaml` and `../packs/catalog.yaml`, and the docs come from
`../docs` (copied and link-rewritten by `scripts/sync-docs.mjs` at build
time), so the site cannot claim more than the repository does.

```sh
pnpm install
pnpm dev        # http://localhost:4321
pnpm build      # static output in dist/
```

## Deploying on Vercel

Import the repository in Vercel and set the project's **Root Directory** to
`site`. `vercel.json` sets the build. Every push to `main` then redeploys.
