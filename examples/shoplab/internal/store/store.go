// Package store is the Postgres implementation of the shop interfaces. Two
// planted bottlenecks live here: the N+1 product listing (SHOPLAB_FIX_N1) and
// the oversell race in checkout (SHOPLAB_FIX_RACE).
package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ivan825/Stampede/examples/shoplab/internal/shop"
)

// Options selects fixed or planted behaviour.
type Options struct {
	FixN1   bool
	FixRace bool
	// OnOversell is called after a checkout commits with the number of
	// units it sold beyond the product's inventory (0 calls when none).
	OnOversell func(productID int64, units int64)
}

// Store implements shop.Catalog, shop.Users, shop.Orders and shop.Inventory.
type Store struct {
	pool *pgxpool.Pool
	opts Options
}

var (
	_ shop.Catalog   = (*Store)(nil)
	_ shop.Users     = (*Store)(nil)
	_ shop.Orders    = (*Store)(nil)
	_ shop.Inventory = (*Store)(nil)
)

// New creates a Store.
func New(pool *pgxpool.Pool, opts Options) *Store {
	if opts.OnOversell == nil {
		opts.OnOversell = func(int64, int64) {}
	}
	return &Store{pool: pool, opts: opts}
}

// Pagination limits shared by the listing endpoints.
const (
	DefaultPerPage = 20
	MaxPerPage     = 100
)

// productFilter builds the WHERE clause for the listing. It is shared by the
// N+1 and fixed paths so both return identical results.
func productFilter(p shop.ListParams) (string, []any) {
	var conds []string
	var args []any
	if q := strings.TrimSpace(p.Query); q != "" {
		args = append(args, "%"+escapeLike(q)+"%")
		conds = append(conds, fmt.Sprintf("p.name ILIKE $%d", len(args)))
	}
	if c := strings.TrimSpace(p.Category); c != "" {
		if id, err := strconv.ParseInt(c, 10, 64); err == nil {
			args = append(args, id)
			conds = append(conds, fmt.Sprintf("p.category_id = $%d", len(args)))
		} else {
			args = append(args, strings.ToLower(c))
			conds = append(conds, fmt.Sprintf("p.category_id = (SELECT id FROM categories WHERE slug = $%d)", len(args)))
		}
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ListProducts returns a page of products with category and rating.
func (s *Store) ListProducts(ctx context.Context, p shop.ListParams) (shop.ProductPage, error) {
	page, perPage := shop.Clamp(p.Page, p.PerPage, DefaultPerPage, MaxPerPage)
	where, args := productFilter(p)
	out := shop.ProductPage{Page: page, PerPage: perPage, Products: []shop.ProductSummary{}}

	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM products p"+where, args...).Scan(&out.Total); err != nil {
		return out, fmt.Errorf("count products: %w", err)
	}
	if out.Total == 0 {
		return out, nil
	}
	limitArgs := append(slices.Clone(args), perPage, (page-1)*perPage)
	limit := fmt.Sprintf(" ORDER BY p.id LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)

	var err error
	if s.opts.FixN1 {
		out.Products, err = s.listJoined(ctx, where, limit, limitArgs)
	} else {
		out.Products, err = s.listNPlusOne(ctx, where, limit, limitArgs)
	}
	return out, err
}

// listNPlusOne is the planted bottleneck: one query for the page, then one
// query per product for its category and another for its rating aggregate.
// A 20-item page costs 41 round trips and 41 pool acquisitions.
func (s *Store) listNPlusOne(ctx context.Context, where, limit string, args []any) ([]shop.ProductSummary, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT p.id, p.sku, p.name, p.price_cents, p.stock, p.category_id FROM products p"+where+limit, args...)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	products, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (shop.ProductSummary, error) {
		var p shop.ProductSummary
		err := r.Scan(&p.ID, &p.SKU, &p.Name, &p.PriceCents, &p.Stock, &p.Category.ID)
		return p, err
	})
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	for i := range products {
		p := &products[i]
		p.Price = shop.Dollars(p.PriceCents)
		if err := s.pool.QueryRow(ctx,
			"SELECT id, name, slug FROM categories WHERE id = $1", p.Category.ID,
		).Scan(&p.Category.ID, &p.Category.Name, &p.Category.Slug); err != nil {
			return nil, fmt.Errorf("product %d category: %w", p.ID, err)
		}
		if err := s.pool.QueryRow(ctx,
			"SELECT coalesce(avg(rating), 0)::float8, count(*) FROM reviews WHERE product_id = $1", p.ID,
		).Scan(&p.AverageRating, &p.ReviewCount); err != nil {
			return nil, fmt.Errorf("product %d rating: %w", p.ID, err)
		}
		p.AverageRating = round2(p.AverageRating)
	}
	return products, nil
}

// listJoined is the fix: a single statement with a join for the category and
// a LATERAL aggregate for the rating.
func (s *Store) listJoined(ctx context.Context, where, limit string, args []any) ([]shop.ProductSummary, error) {
	sql := `SELECT p.id, p.sku, p.name, p.price_cents, p.stock,
	               c.id, c.name, c.slug,
	               r.avg_rating, r.review_count
	        FROM (SELECT p.id, p.sku, p.name, p.price_cents, p.stock, p.category_id
	              FROM products p` + where + limit + `) p
	        JOIN categories c ON c.id = p.category_id
	        LEFT JOIN LATERAL (
	            SELECT coalesce(avg(rating), 0)::float8 AS avg_rating, count(*) AS review_count
	            FROM reviews WHERE product_id = p.id
	        ) r ON true
	        ORDER BY p.id`
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	products, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (shop.ProductSummary, error) {
		var p shop.ProductSummary
		err := r.Scan(&p.ID, &p.SKU, &p.Name, &p.PriceCents, &p.Stock,
			&p.Category.ID, &p.Category.Name, &p.Category.Slug, &p.AverageRating, &p.ReviewCount)
		p.Price = shop.Dollars(p.PriceCents)
		p.AverageRating = round2(p.AverageRating)
		return p, err
	})
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	return products, nil
}

// stopwords are excluded from review highlights.
var stopwords = []string{
	"this", "that", "with", "have", "they", "were", "from", "been", "very", "just",
	"than", "them", "then", "when", "what", "your", "will", "would", "there", "their",
	"about", "after", "really", "also", "only", "much", "more", "some", "into", "does",
	"didn", "doesn", "over", "even", "because", "which", "could", "should", "these", "those",
}

// ProductDetail computes the product page from scratch. The review summary,
// especially the "highlights" (most used words across all reviews), is a
// genuinely CPU-heavy aggregate for popular products with thousands of
// reviews; this is what the cache protects.
func (s *Store) ProductDetail(ctx context.Context, id int64) (shop.ProductDetail, error) {
	var d shop.ProductDetail
	err := s.pool.QueryRow(ctx, `
		SELECT p.id, p.sku, p.name, p.description, p.price_cents, p.stock, c.id, c.name, c.slug
		FROM products p JOIN categories c ON c.id = p.category_id
		WHERE p.id = $1`, id,
	).Scan(&d.ID, &d.SKU, &d.Name, &d.Description, &d.PriceCents, &d.Stock,
		&d.Category.ID, &d.Category.Name, &d.Category.Slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, shop.ErrNotFound
	}
	if err != nil {
		return d, fmt.Errorf("product %d: %w", id, err)
	}
	d.Price = shop.Dollars(d.PriceCents)

	rs := &d.Reviews
	var h [5]int64
	err = s.pool.QueryRow(ctx, `
		SELECT count(*), coalesce(avg(rating), 0)::float8,
		       count(*) FILTER (WHERE rating = 1), count(*) FILTER (WHERE rating = 2),
		       count(*) FILTER (WHERE rating = 3), count(*) FILTER (WHERE rating = 4),
		       count(*) FILTER (WHERE rating = 5)
		FROM reviews WHERE product_id = $1`, id,
	).Scan(&rs.Count, &rs.AverageRating, &h[0], &h[1], &h[2], &h[3], &h[4])
	if err != nil {
		return d, fmt.Errorf("product %d review stats: %w", id, err)
	}
	rs.AverageRating = round2(rs.AverageRating)
	rs.Histogram = map[string]int64{"1": h[0], "2": h[1], "3": h[2], "4": h[3], "5": h[4]}

	rows, err := s.pool.Query(ctx, `
		SELECT term, count(*) AS n
		FROM reviews r, regexp_split_to_table(lower(r.title || ' ' || r.body), '[^a-z]+') AS term
		WHERE r.product_id = $1 AND length(term) >= 4 AND term <> ALL($2::text[])
		GROUP BY term
		ORDER BY n DESC, term
		LIMIT 5`, id, stopwords)
	if err != nil {
		return d, fmt.Errorf("product %d highlights: %w", id, err)
	}
	rs.Highlights, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (shop.TermCount, error) {
		var t shop.TermCount
		return t, r.Scan(&t.Term, &t.Count)
	})
	if err != nil {
		return d, fmt.Errorf("product %d highlights: %w", id, err)
	}

	rows, err = s.pool.Query(ctx, `
		SELECT r.id, r.rating, r.title, r.body, u.name, r.created_at
		FROM reviews r JOIN users u ON u.id = r.user_id
		WHERE r.product_id = $1
		ORDER BY r.created_at DESC
		LIMIT 5`, id)
	if err != nil {
		return d, fmt.Errorf("product %d latest reviews: %w", id, err)
	}
	rs.Latest, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (shop.Review, error) {
		var rv shop.Review
		return rv, r.Scan(&rv.ID, &rv.Rating, &rv.Title, &rv.Body, &rv.Author, &rv.CreatedAt)
	})
	if err != nil {
		return d, fmt.Errorf("product %d latest reviews: %w", id, err)
	}
	d.GeneratedAt = time.Now().UTC()
	return d, nil
}

// ProductBriefs loads name, price and stock for the given ids.
func (s *Store) ProductBriefs(ctx context.Context, ids []int64) (map[int64]shop.ProductBrief, error) {
	out := make(map[int64]shop.ProductBrief, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, "SELECT id, name, price_cents, stock FROM products WHERE id = ANY($1)", ids)
	if err != nil {
		return nil, fmt.Errorf("product briefs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var b shop.ProductBrief
		if err := rows.Scan(&b.ID, &b.Name, &b.PriceCents, &b.Stock); err != nil {
			return nil, err
		}
		out[b.ID] = b
	}
	return out, rows.Err()
}

// UserByEmail looks a user up by (case-insensitive) email.
func (s *Store) UserByEmail(ctx context.Context, email string) (shop.User, error) {
	return s.user(ctx, "email = lower($1)", strings.TrimSpace(email))
}

// UserByID looks a user up by id.
func (s *Store) UserByID(ctx context.Context, id int64) (shop.User, error) {
	return s.user(ctx, "id = $1", id)
}

func (s *Store) user(ctx context.Context, cond string, arg any) (shop.User, error) {
	var u shop.User
	err := s.pool.QueryRow(ctx,
		"SELECT id, email, name, password_hash, created_at FROM users WHERE "+cond, arg,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, shop.ErrNotFound
	}
	return u, err
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}
