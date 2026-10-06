// Package seed fills the database with deterministic demo data: categories,
// products (ids 1-10 are low-stock for the oversell demo, ids 1..1% are
// "popular" with thousands of reviews for the cache demo), users with a known
// password, order history and reviews.
package seed

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/Ivan825/Stampede/examples/shoplab/internal/shop"
)

// Password is every seeded user's password.
const Password = "shoplab-pass"

// BcryptCost is deliberately the minimum (4) so that hashing during login is
// ~1 ms and never the bottleneck being measured. Do not copy this into a real
// system; production cost should be 10-12+.
const BcryptCost = bcrypt.MinCost

// RNG seeds: identical options always produce identical data (password hash
// salts excepted, which bcrypt draws from crypto/rand).
const (
	rngSeed1 = 42
	rngSeed2 = 2026
)

// baseTime anchors generated timestamps so data does not depend on when the
// seeder runs.
var baseTime = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// Options controls dataset size.
type Options struct {
	Products      int
	Users         int
	OrdersPerUser int
	// HotReviews is the review count for each popular product (the first
	// 1% of products, at least 10). Other products get 0-8 reviews.
	HotReviews int
	// Reset truncates existing data first. Without it, seeding a non-empty
	// database fails unless IfEmpty is set, in which case it is a no-op.
	Reset   bool
	IfEmpty bool
}

// Defaults matches `shoplab seed` with no flags.
func Defaults() Options {
	return Options{Products: 10000, Users: 1000, OrdersPerUser: 20, HotReviews: 2000}
}

// Stats summarises a seeding run.
type Stats struct {
	Skipped    bool
	Categories int
	Products   int
	Users      int
	Orders     int
	OrderItems int
	Reviews    int
	Duration   time.Duration
}

// UserEmail returns the email of seeded user i (1-based).
func UserEmail(i int) string { return fmt.Sprintf("user%04d@shoplab.test", i) }

// WriteUsersCSV writes the email,password data-feeder file for n users.
func WriteUsersCSV(w io.Writer, n int) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"email", "password"}); err != nil {
		return err
	}
	for i := 1; i <= n; i++ {
		if err := cw.Write([]string{UserEmail(i), Password}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// HotProducts is how many leading product ids are "popular".
func HotProducts(products int) int { return min(products, max(10, products/100)) }

// Categories in id order.
var Categories = []struct{ Name, Slug string }{
	{"Shoes", "shoes"}, {"Apparel", "apparel"}, {"Electronics", "electronics"},
	{"Home & Kitchen", "home-kitchen"}, {"Books", "books"}, {"Toys", "toys"},
	{"Sports & Outdoors", "sports-outdoors"}, {"Beauty", "beauty"}, {"Garden", "garden"},
	{"Grocery", "grocery"}, {"Automotive", "automotive"}, {"Office", "office"},
}

var nouns = [][]string{
	{"Running Shoe", "Trail Shoe", "Sneaker", "Hiking Boot", "Sandal", "Loafer", "Court Shoe", "Slip-On Shoe"},
	{"T-Shirt", "Hoodie", "Rain Jacket", "Chinos", "Wool Sweater", "Denim Jacket", "Polo Shirt", "Fleece"},
	{"Headphones", "Bluetooth Speaker", "USB-C Charger", "Smartwatch", "Webcam", "Keyboard", "Monitor Arm", "Power Bank"},
	{"Chef Knife", "Cast Iron Pan", "Coffee Grinder", "Kettle", "Blender", "Cutting Board", "Dutch Oven", "Toaster"},
	{"Novel", "Cookbook", "Field Guide", "Atlas", "Poetry Collection", "Biography", "Notebook Journal", "Comic"},
	{"Puzzle", "Building Blocks", "Board Game", "Plush Bear", "Kite", "Train Set", "Yo-Yo", "Card Game"},
	{"Yoga Mat", "Tent", "Water Bottle", "Daypack", "Bike Light", "Climbing Rope", "Sleeping Bag", "Dumbbell"},
	{"Face Cream", "Shampoo", "Lip Balm", "Sunscreen", "Hair Brush", "Body Wash", "Perfume", "Nail Kit"},
	{"Hose", "Trowel", "Planter", "Seed Kit", "Pruning Shears", "Bird Feeder", "Watering Can", "Garden Gloves"},
	{"Olive Oil", "Coffee Beans", "Green Tea", "Pasta", "Honey", "Granola", "Hot Sauce", "Dark Chocolate"},
	{"Floor Mats", "Phone Mount", "Jump Starter", "Car Wax", "Seat Cover", "Tire Gauge", "Dash Cam", "Wiper Blades"},
	{"Desk Lamp", "Stapler", "Gel Pens", "Desk Organizer", "Whiteboard", "Office Chair", "Paper Shredder", "Label Maker"},
}

var adjectives = []string{
	"Classic", "Ultra", "Eco", "Pro", "Compact", "Deluxe", "Everyday", "Urban", "Alpine", "Coastal",
	"Vintage", "Modern", "Lightweight", "Rugged", "Premium", "Essential", "Smart", "Cozy", "Swift", "Bold",
}

var colors = []string{"Black", "White", "Navy", "Olive", "Red", "Grey", "Sand", "Teal", "Blue", "Green"}

var firstNames = []string{"Ava", "Ben", "Chloe", "Dev", "Ema", "Finn", "Gia", "Hugo", "Isla", "Jai",
	"Kira", "Leo", "Maya", "Nico", "Omar", "Priya", "Quinn", "Rosa", "Sam", "Tara", "Uma", "Vik", "Wren", "Yusuf", "Zoe"}

var lastNames = []string{"Sharma", "Smith", "Garcia", "Chen", "Okafor", "Novak", "Silva", "Kim", "Patel",
	"Müller", "Rossi", "Haddad", "Nguyen", "Kowalski", "Jensen", "Ito", "Costa", "Murphy", "Reyes", "Singh"}

var reviewTitles = []string{"Great value", "Exactly as described", "Would buy again", "Not bad", "Disappointed",
	"Love it", "Solid choice", "Better than expected", "Okay for the price", "Fantastic quality", "Meh", "Five stars"}

var reviewWords = strings.Fields(`comfortable durable quality sturdy cheap flimsy perfect sizing fits small
large battery shipping arrived quickly packaging broke returned gift daughter husband wife friend kitchen
office daily weekend travel hiking running cooking reading design color material stitching strap handle
lid noise sound bright warm lightweight heavy price value expensive bargain recommend favorite works great
excellent poor average instructions setup easy difficult minutes hours weeks months years replacement
customer service support smell texture taste fresh soft rough smooth grip waterproof scratch charger cable
screen button zipper pocket sole laces heel toes ankle cushion support arch fabric seams wash dryer shrink
fade stain clean assembly screws wobbly stable solid premium budget upgrade original version model newer
older second third purchase bought ordered store online compared brand cheaper pricier happy satisfied
the and a it is was for with this that very but not too really just also so my of to in on`)

type product struct {
	id         int64
	sku, name  string
	desc       string
	categoryID int64
	price      int64
	stock      int64
	sold       int64
}

type order struct {
	userID  int64
	created time.Time
	items   []orderItem
	sub     int64
}

type orderItem struct {
	productID int64
	qty       int
	price     int64
}

// Run seeds the database.
func Run(ctx context.Context, pool *pgxpool.Pool, opts Options, log *slog.Logger) (Stats, error) {
	start := time.Now()
	var st Stats
	if opts.Products < shop.LowStockProducts+1 || opts.Users < 1 || opts.OrdersPerUser < 0 || opts.HotReviews < 0 {
		return st, fmt.Errorf("seed: need at least %d products, 1 user and non-negative counts", shop.LowStockProducts+1)
	}

	var existing int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM products").Scan(&existing); err != nil {
		return st, fmt.Errorf("seed: check existing data: %w", err)
	}
	if existing > 0 && !opts.Reset {
		if opts.IfEmpty {
			log.Info("database already seeded; skipping", "products", existing)
			st.Skipped = true
			return st, nil
		}
		return st, fmt.Errorf("seed: database already has %d products; pass --reset to wipe it", existing)
	}

	rng := rand.New(rand.NewPCG(rngSeed1, rngSeed2))
	products := genProducts(rng, opts.Products)
	log.Info("generating users", "users", opts.Users, "bcrypt_cost", BcryptCost)
	userNames, hashes, err := genUsers(rng, opts.Users)
	if err != nil {
		return st, err
	}
	orders := genOrders(rng, products, opts.Users, opts.OrdersPerUser)

	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if opts.Reset {
			if _, err := tx.Exec(ctx, "TRUNCATE order_items, orders, reviews, products, users, categories RESTART IDENTITY CASCADE"); err != nil {
				return err
			}
		}
		var err error
		if st.Categories, err = copyCategories(ctx, tx); err != nil {
			return fmt.Errorf("categories: %w", err)
		}
		if st.Products, err = copyProducts(ctx, tx, products); err != nil {
			return fmt.Errorf("products: %w", err)
		}
		if st.Users, err = copyUsers(ctx, tx, userNames, hashes); err != nil {
			return fmt.Errorf("users: %w", err)
		}
		if st.Orders, st.OrderItems, err = copyOrders(ctx, tx, orders); err != nil {
			return fmt.Errorf("orders: %w", err)
		}
		if st.Reviews, err = copyReviews(ctx, tx, rng, opts, len(userNames)); err != nil {
			return fmt.Errorf("reviews: %w", err)
		}
		for _, tbl := range []string{"categories", "products", "users", "orders", "reviews"} {
			if _, err := tx.Exec(ctx, fmt.Sprintf(
				"SELECT setval(pg_get_serial_sequence('%[1]s', 'id'), coalesce((SELECT max(id) FROM %[1]s), 0) + 1, false)", tbl)); err != nil {
				return fmt.Errorf("setval %s: %w", tbl, err)
			}
		}
		return nil
	})
	if err != nil {
		return st, fmt.Errorf("seed: %w", err)
	}
	if _, err := pool.Exec(ctx, "VACUUM (ANALYZE)"); err != nil {
		return st, fmt.Errorf("seed: vacuum analyze: %w", err)
	}
	st.Duration = time.Since(start)
	return st, nil
}

func genProducts(rng *rand.Rand, n int) []product {
	out := make([]product, n)
	for i := range out {
		id := int64(i + 1)
		cat := rng.IntN(len(Categories))
		noun := nouns[cat][rng.IntN(len(nouns[cat]))]
		adj := adjectives[rng.IntN(len(adjectives))]
		color := colors[rng.IntN(len(colors))]
		p := product{
			id:         id,
			sku:        fmt.Sprintf("SL-%06d", id),
			name:       fmt.Sprintf("%s %s %s", adj, color, noun),
			categoryID: int64(cat + 1),
			price:      int64(299 + rng.IntN(29700)),
			stock:      int64(20 + rng.IntN(481)),
		}
		p.desc = fmt.Sprintf("The %s %s is a %s pick from our %s range. %s finish, built for %s use.",
			strings.ToLower(adj), strings.ToLower(noun), strings.ToLower(adjectives[rng.IntN(len(adjectives))]),
			Categories[cat].Name, color, []string{"everyday", "heavy", "occasional", "outdoor", "professional"}[rng.IntN(5)])
		if id <= shop.LowStockProducts {
			p.stock = shop.LowStockQty
		}
		out[i] = p
	}
	return out
}

func genUsers(rng *rand.Rand, n int) ([]string, []string, error) {
	names := make([]string, n)
	for i := range names {
		names[i] = firstNames[rng.IntN(len(firstNames))] + " " + lastNames[rng.IntN(len(lastNames))]
	}
	hashes := make([]string, n)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	work := make(chan int)
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for i := range work {
				h, err := bcrypt.GenerateFromPassword([]byte(Password), BcryptCost)
				if err != nil {
					mu.Lock()
					firstErr = err
					mu.Unlock()
					continue
				}
				hashes[i] = string(h)
			}
		})
	}
	for i := range n {
		work <- i
	}
	close(work)
	wg.Wait()
	return names, hashes, firstErr
}

func genOrders(rng *rand.Rand, products []product, users, perUser int) []order {
	orders := make([]order, 0, users*perUser)
	// Historic orders never touch the low-stock demo products, so their
	// inventory ledger starts clean.
	first := shop.LowStockProducts
	for u := 1; u <= users; u++ {
		for range perUser {
			o := order{
				userID:  int64(u),
				created: baseTime.Add(-time.Duration(rng.Int64N(int64(730 * 24 * time.Hour)))),
			}
			nItems := 1 + rng.IntN(4)
			for len(o.items) < nItems {
				p := &products[first+rng.IntN(len(products)-first)]
				if slices.ContainsFunc(o.items, func(it orderItem) bool { return it.productID == p.id }) {
					continue
				}
				q := 1 + rng.IntN(3)
				o.items = append(o.items, orderItem{productID: p.id, qty: q, price: p.price})
				o.sub += p.price * int64(q)
				p.sold += int64(q)
			}
			orders = append(orders, o)
		}
	}
	// Ids follow time, as they would in a real system, and users' orders
	// are interleaved across the table.
	slices.SortStableFunc(orders, func(a, b order) int { return a.created.Compare(b.created) })
	return orders
}

func copyCategories(ctx context.Context, tx pgx.Tx) (int, error) {
	rows := make([][]any, len(Categories))
	for i, c := range Categories {
		rows[i] = []any{int64(i + 1), c.Name, c.Slug}
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"categories"}, []string{"id", "name", "slug"}, pgx.CopyFromRows(rows))
	return int(n), err
}

func copyProducts(ctx context.Context, tx pgx.Tx, ps []product) (int, error) {
	created := baseTime.Add(-3 * 365 * 24 * time.Hour)
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"products"},
		[]string{"id", "sku", "name", "description", "category_id", "price_cents", "stock", "units_received", "units_sold", "created_at"},
		pgx.CopyFromSlice(len(ps), func(i int) ([]any, error) {
			p := ps[i]
			return []any{p.id, p.sku, p.name, p.desc, p.categoryID, p.price, p.stock, p.stock + p.sold, p.sold,
				created.Add(time.Duration(i) * time.Minute)}, nil
		}))
	return int(n), err
}

func copyUsers(ctx context.Context, tx pgx.Tx, names, hashes []string) (int, error) {
	created := baseTime.Add(-2 * 365 * 24 * time.Hour)
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"users"}, []string{"id", "email", "name", "password_hash", "created_at"},
		pgx.CopyFromSlice(len(names), func(i int) ([]any, error) {
			return []any{int64(i + 1), UserEmail(i + 1), names[i], hashes[i], created.Add(time.Duration(i) * time.Hour)}, nil
		}))
	return int(n), err
}

func copyOrders(ctx context.Context, tx pgx.Tx, orders []order) (int, int, error) {
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"orders"},
		[]string{"id", "user_id", "status", "subtotal_cents", "tax_cents", "shipping_cents", "total_cents", "shipping_address", "created_at"},
		pgx.CopyFromSlice(len(orders), func(i int) ([]any, error) {
			o := orders[i]
			tax, ship, total := shop.Totals(o.sub)
			status := "delivered"
			if baseTime.Sub(o.created) < 7*24*time.Hour {
				status = "shipped"
			}
			addr := fmt.Sprintf("%d Example Road, Unit %d, Testville", 1+o.userID%400, 1+i%60)
			return []any{int64(i + 1), o.userID, status, o.sub, tax, ship, total, addr, o.created}, nil
		}))
	if err != nil {
		return 0, 0, err
	}
	oi, li := 0, 0
	items, err := tx.CopyFrom(ctx, pgx.Identifier{"order_items"}, []string{"order_id", "product_id", "qty", "unit_price_cents"},
		pgx.CopyFromFunc(func() ([]any, error) {
			for oi < len(orders) && li >= len(orders[oi].items) {
				oi, li = oi+1, 0
			}
			if oi >= len(orders) {
				return nil, nil
			}
			it := orders[oi].items[li]
			li++
			return []any{int64(oi + 1), it.productID, it.qty, it.price}, nil
		}))
	return int(n), int(items), err
}

func copyReviews(ctx context.Context, tx pgx.Tx, rng *rand.Rand, opts Options, users int) (int, error) {
	hot := HotProducts(opts.Products)
	pid, left := int64(0), 0
	next := func() bool {
		for left == 0 {
			pid++
			if pid > int64(opts.Products) {
				return false
			}
			if pid <= int64(hot) {
				left = opts.HotReviews
			} else {
				left = rng.IntN(9)
			}
		}
		left--
		return true
	}
	var words []string
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"reviews"}, []string{"product_id", "user_id", "rating", "title", "body", "created_at"},
		pgx.CopyFromFunc(func() ([]any, error) {
			if !next() {
				return nil, nil
			}
			rating := pickRating(rng)
			words = words[:0]
			for range 12 + rng.IntN(24) {
				words = append(words, reviewWords[rng.IntN(len(reviewWords))])
			}
			body := strings.ToUpper(words[0][:1]) + words[0][1:] + " " + strings.Join(words[1:], " ") + "."
			created := baseTime.Add(-time.Duration(rng.Int64N(int64(700 * 24 * time.Hour))))
			return []any{pid, int64(1 + rng.IntN(users)), int16(rating), reviewTitles[rng.IntN(len(reviewTitles))], body, created}, nil
		}))
	return int(n), err
}

// pickRating skews towards positive reviews like real shops.
func pickRating(rng *rand.Rand) int {
	switch r := rng.IntN(100); {
	case r < 5:
		return 1
	case r < 12:
		return 2
	case r < 27:
		return 3
	case r < 60:
		return 4
	default:
		return 5
	}
}
