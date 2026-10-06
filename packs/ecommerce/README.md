# E-commerce pack

Journeys, stresses and targets for shop and marketplace APIs.

| File | What it tests |
|---|---|
| `journeys/shop-mix.yaml` | Everyday mix: browse 60%, search 20%, order history 15%, checkout 5% (30% abandon the cart) |
| `journeys/failed-login.yaml` | Wrong passwords are rejected quickly and cleanly |
| `stresses/flash-sale-spike.yaml` | Tenfold traffic spike on sale and popular product pages |
| `stresses/last-item-contention.yaml` | Many buyers racing for scarce stock; 409 is the right answer once stock runs out |
| `targets.yaml` | Default targets: p95 under 500ms, under 1% errors, checkout held tighter |

The journeys follow ShopLab's API (`/api/products`, `/api/login`,
`/api/cart`, `/api/checkout`, `/api/orders`, JSON with a `products`
array and a bearer token). Every journey is dry-run against ShopLab in CI.
For your own shop, change the paths and the JSONPath extractors to match
your API.

```sh
stampede init --target http://localhost:8090      # detects this pack
stampede run stampede/shop-mix.yaml -e TARGET_URL=http://localhost:8090
```
