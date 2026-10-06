// Scenario YAML used by the mock server's fixtures.

export const shopSmoke = `apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: shop-smoke
  description: Browse the catalogue and sign in, at a steady rate.
  tags: [smoke, ci]
target:
  baseURL: \${env.TARGET_URL}
journeys:
  - name: browse
    weight: 9
    steps:
      - get: /api/products?page=\${rand(1, 20)}
        check: { status: 200 }
        extract: { productId: "$.items[0].id" }
      - think: 1s..3s
      - get: /api/products/\${productId}
  - name: login
    weight: 1
    steps:
      - post: /api/login
        json: { email: "\${data.users.email}", password: "\${data.users.password}" }
        extract: { token: "$.token" }
      - get: /api/me
        headers: { Authorization: "Bearer \${token}" }
data:
  users: { csv: users.csv, mode: unique }
load:
  mode: rate
  rate: 50/s
  duration: 2m
targets:
  - http.p95 < 300ms
  - errors < 1%
`;

export const checkoutStress = `apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: checkout-stress
  description: Shoppers add to cart and check out while load ramps up.
  tags: [stress, checkout]
target:
  baseURL: \${env.TARGET_URL}
  headers: { X-Api-Key: "\${secret.API_KEY}" }
journeys:
  - name: shopper
    weight: 7
    target: { p95: 400ms, errors: 1% }
    steps:
      - name: home
        get: /
        check: { status: 200 }
      - think: 1s..2s
      - name: search
        get: /api/search?q=\${pick(["boots", "socks", "jacket"])}
        extract: { productId: "$.results[0].id" }
      - branch:
          - weight: 3
            name: buy
            steps:
              - name: add to cart
                post: /api/cart
                json: { productId: "\${productId}", qty: 1 }
                check: { status: [200, 201] }
              - think: 2s
              - group: checkout
                steps:
                  - name: shipping
                    post: /api/checkout/shipping
                  - name: pay
                    post: /api/checkout/pay
                    json: { card: "\${secret.STRIPE_TEST_KEY}" }
                    check: { status: 200, maxLatency: 1s }
          - weight: 7
            name: browse more
            steps:
              - loop: 3
                steps:
                  - name: product page
                    get: /api/products/\${rand(1, 500)}
                  - think: 1s..4s
  - name: order-status
    weight: 3
    steps:
      - name: orders
        get: /api/orders?limit=10
        check: { status: 200 }
load:
  shape: stress
  mode: rate
  start: 20/s
  max: 400/s
  duration: 5m
  maxVUs: 2000
targets:
  - http.p95 < 400ms
  - errors < 1%
  - checkout.p99 < 1s
`;

export const searchSoak = `apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: search-soak
  description: Hours of steady search traffic to catch leaks.
  tags: [soak, search]
target:
  baseURL: \${env.TARGET_URL}
journeys:
  - name: searcher
    steps:
      - name: suggest
        get: /api/suggest?q=\${randString(3)}
      - think: 500ms..1500ms
      - name: search
        get: /api/search?q=\${randString(5)}
        check: { status: 200, json: { "$.total": exists } }
load:
  shape: soak
  mode: vus
  vus: 120
  duration: 2h
targets:
  - http.p99 < 800ms
  - errors < 0.1%
`;

export const catalogBreakpoint = `apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: catalog-breakpoint
  description: Find the highest rate the catalogue holds within targets.
  tags: [breakpoint]
target:
  baseURL: \${env.TARGET_URL}
journeys:
  - name: catalogue
    steps:
      - name: list
        get: /api/products?page=\${rand(1, 50)}
        check: { status: 200 }
      - name: detail
        get: /api/products/\${rand(1, 500)}
load:
  shape: breakpoint
  mode: rate
  start: 50/s
  max: 1000/s
  steps: 10
  stepDuration: 30s
targets:
  - http.p95 < 250ms
  - errors < 1%
`;

export const paymentsBaseline = `apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: payments-baseline
  tags: [baseline]
target:
  baseURL: \${env.TARGET_URL}
journeys:
  - name: authorise
    steps:
      - name: authorise
        post: /v1/authorise
        json: { amount: 1999, currency: GBP }
        check: { status: 201 }
        extract: { id: "$.id" }
      - name: capture
        post: /v1/payments/\${id}/capture
load:
  shape: baseline
  mode: rate
  rate: 30/s
  duration: 10m
targets:
  - http.p99 < 500ms
  - errors < 0.5%
`;
