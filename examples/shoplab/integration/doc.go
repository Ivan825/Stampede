// Package integration holds ShopLab's integration tests. They need a real
// Postgres and Redis and only build with the "integration" tag:
//
//	docker compose up -d postgres redis
//	DATABASE_URL=postgres://shoplab:shoplab@127.0.0.1:55432/shoplab?sslmode=disable \
//	REDIS_URL=redis://127.0.0.1:56379/0 \
//	go test -tags integration -v ./integration/
//
// The tests seed a small dataset if the database is empty, add orders,
// reset stock of products 1-10, flush the product cache and toggle the
// orders index (restoring its previous state). Do not point them at data
// you care about.
package integration
