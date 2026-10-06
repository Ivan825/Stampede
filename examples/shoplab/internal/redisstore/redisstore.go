// Package redisstore holds ShopLab's Redis-backed pieces: carts and the
// byte store underneath the product cache.
package redisstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Ivan825/Stampede/examples/shoplab/internal/cache"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/shop"
)

// CartPrefix namespaces carts. Everything ShopLab writes to Redis lives
// under "shoplab:"; product-detail cache keys use cache.ProductKeyPrefix.
const CartPrefix = "shoplab:cart:"

// Connect parses a redis:// URL and returns a client.
func Connect(url string) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis: parse REDIS_URL: %w", err)
	}
	if opt.PoolSize < 50 {
		opt.PoolSize = 50
	}
	return redis.NewClient(opt), nil
}

// Carts stores each cart as a Redis hash productId -> qty, expiring together
// with the session.
type Carts struct {
	rdb *redis.Client
	ttl time.Duration
}

var _ shop.Carts = (*Carts)(nil)

// NewCarts creates a cart store whose keys expire after ttl of inactivity.
func NewCarts(rdb *redis.Client, ttl time.Duration) *Carts { return &Carts{rdb: rdb, ttl: ttl} }

// SetQty sets the quantity of a product in the cart (upsert).
func (c *Carts) SetQty(ctx context.Context, cartID string, productID int64, qty int) error {
	key := CartPrefix + cartID
	_, err := c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, key, strconv.FormatInt(productID, 10), qty)
		p.Expire(ctx, key, c.ttl)
		return nil
	})
	return err
}

// Remove deletes a product from the cart. Removing an absent product is not
// an error.
func (c *Carts) Remove(ctx context.Context, cartID string, productID int64) error {
	return c.rdb.HDel(ctx, CartPrefix+cartID, strconv.FormatInt(productID, 10)).Err()
}

// Lines returns the cart contents.
func (c *Carts) Lines(ctx context.Context, cartID string) ([]shop.CartLine, error) {
	m, err := c.rdb.HGetAll(ctx, CartPrefix+cartID).Result()
	if err != nil {
		return nil, err
	}
	out := make([]shop.CartLine, 0, len(m))
	for k, v := range m {
		id, err1 := strconv.ParseInt(k, 10, 64)
		qty, err2 := strconv.Atoi(v)
		if err1 != nil || err2 != nil {
			continue // ignore garbage rather than failing the cart
		}
		out = append(out, shop.CartLine{ProductID: id, Qty: qty})
	}
	return out, nil
}

// Clear empties the cart.
func (c *Carts) Clear(ctx context.Context, cartID string) error {
	return c.rdb.Del(ctx, CartPrefix+cartID).Err()
}

// KV adapts Redis to cache.KV.
type KV struct{ rdb *redis.Client }

var _ cache.KV = (*KV)(nil)

// NewKV wraps a client.
func NewKV(rdb *redis.Client) *KV { return &KV{rdb: rdb} }

// Get implements cache.KV.
func (k *KV) Get(ctx context.Context, key string) ([]byte, bool, error) {
	b, err := k.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// Set implements cache.KV.
func (k *KV) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return k.rdb.Set(ctx, key, val, ttl).Err()
}

// FlushPrefix deletes every key starting with prefix and returns the count.
// It uses SCAN + UNLINK so it never blocks Redis.
func FlushPrefix(ctx context.Context, rdb *redis.Client, prefix string) (int, error) {
	n := 0
	iter := rdb.Scan(ctx, 0, prefix+"*", 1000).Iterator()
	batch := make([]string, 0, 500)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := rdb.Unlink(ctx, batch...).Err(); err != nil {
			return err
		}
		n += len(batch)
		batch = batch[:0]
		return nil
	}
	for iter.Next(ctx) {
		batch = append(batch, iter.Val())
		if len(batch) == cap(batch) {
			if err := flush(); err != nil {
				return n, err
			}
		}
	}
	if err := iter.Err(); err != nil {
		return n, err
	}
	return n, flush()
}
