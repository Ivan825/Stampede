-- ShopLab base schema.
--
-- Deliberate omissions (planted bottlenecks, see README):
--   * orders has NO index on (user_id, created_at). It is created or dropped
--     at startup depending on SHOPLAB_FIX_INDEX.
--   * products.stock has NO CHECK (stock >= 0) so that the checkout race can
--     actually oversell. The fixed checkout guards with WHERE stock >= qty.
--
-- Inventory bookkeeping: units_received and units_sold are an append-only
-- style ledger kept on the product row. In a correct system
--     stock = units_received - units_sold
-- always holds; when it does not, units were sold that did not exist.

CREATE TABLE categories (
    id   serial PRIMARY KEY,
    name text NOT NULL UNIQUE,
    slug text NOT NULL UNIQUE
);

CREATE TABLE products (
    id             bigserial PRIMARY KEY,
    sku            text        NOT NULL UNIQUE,
    name           text        NOT NULL,
    description    text        NOT NULL DEFAULT '',
    category_id    integer     NOT NULL REFERENCES categories (id),
    price_cents    bigint      NOT NULL CHECK (price_cents >= 0),
    stock          integer     NOT NULL,
    units_received bigint      NOT NULL DEFAULT 0,
    units_sold     bigint      NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX products_category_idx ON products (category_id);

CREATE TABLE users (
    id            bigserial PRIMARY KEY,
    email         text        NOT NULL UNIQUE,
    name          text        NOT NULL,
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE reviews (
    id         bigserial PRIMARY KEY,
    product_id bigint      NOT NULL REFERENCES products (id),
    user_id    bigint      NOT NULL REFERENCES users (id),
    rating     smallint    NOT NULL CHECK (rating BETWEEN 1 AND 5),
    title      text        NOT NULL,
    body       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX reviews_product_created_idx ON reviews (product_id, created_at DESC);

CREATE TABLE orders (
    id               bigserial PRIMARY KEY,
    user_id          bigint      NOT NULL REFERENCES users (id),
    status           text        NOT NULL DEFAULT 'placed',
    subtotal_cents   bigint      NOT NULL,
    tax_cents        bigint      NOT NULL,
    shipping_cents   bigint      NOT NULL,
    total_cents      bigint      NOT NULL,
    shipping_address text        NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now()
);
-- Intentionally no index on orders (user_id, created_at); see SHOPLAB_FIX_INDEX.

CREATE TABLE order_items (
    order_id         bigint  NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    product_id       bigint  NOT NULL REFERENCES products (id),
    qty              integer NOT NULL CHECK (qty > 0),
    unit_price_cents bigint  NOT NULL,
    PRIMARY KEY (order_id, product_id)
);
CREATE INDEX order_items_product_idx ON order_items (product_id);
