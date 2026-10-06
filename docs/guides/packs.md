# Product packs

A pack is a folder of journeys, stresses and default targets for one kind of
product. Packs need no engine changes: adding a product type is adding a
folder.

```sh
stampede pack list                      # all 20 product types, shipped or planned
stampede init --target http://localhost:8090   # detect, install, dry-run
stampede pack install ecommerce --dir stampede
stampede pack test ecommerce --target http://localhost:8090
```

Only **e-commerce** is shipped today: its journeys are dry-run against
ShopLab on every push. The other 19 are listed as planned until each has a
reference app and passing tests.

## Layout

```
packs/ecommerce/
├── pack.yaml          # name, title, status, protocols, detection hints, reference app
├── journeys/          # everyday journeys (full scenarios)
├── stresses/          # targeted stresses
├── targets.yaml       # suggested targets
├── data/              # feeder files, referenced as ../data/... from scenarios
└── README.md
```

## Detection

`stampede init` fetches the target's home page, response headers and an
OpenAPI document from common paths (`/openapi.yaml`, `/openapi.json`,
`/swagger.json`, `/v3/api-docs`, ...). Each pack's `detect` block scores
matches:

```yaml
detect:
  paths: ["/api/products", "/api/cart", "/api/checkout"]   # 3 points each
  openapiTags: [products, cart, checkout]                   # 2 points each
  htmlMeta: ['og:type" content="product']                   # 3 points each
  headers: { X-Powered-By: shopify }                       # 2 points each
```

The best-scoring shipped pack is proposed and confirmed before anything is
installed.

## Writing a pack

1. Copy `packs/ecommerce` and edit `pack.yaml`.
2. Write journeys as normal scenarios using `${env.TARGET_URL}`.
3. Add a reference app (or mock) and make `stampede pack test` pass against it.
4. Add the directory to the embed list in `packs/embed.go`, set its status to
   `shipped` in `packs/catalog.yaml`, and add it to the CI `packs` job.
