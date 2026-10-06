package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/Ivan825/Stampede/examples/shoplab/internal/shop"
)

// NormalizeLines merges duplicate products, drops non-positive quantities and
// sorts by product id. Sorting gives every checkout the same lock order, so
// concurrent multi-item checkouts cannot deadlock.
func NormalizeLines(lines []shop.CartLine) []shop.CartLine {
	qty := map[int64]int{}
	for _, l := range lines {
		if l.Qty > 0 {
			qty[l.ProductID] += l.Qty
		}
	}
	out := make([]shop.CartLine, 0, len(qty))
	for id, q := range qty {
		out = append(out, shop.CartLine{ProductID: id, Qty: q})
	}
	slices.SortFunc(out, func(a, b shop.CartLine) int { return int(a.ProductID - b.ProductID) })
	return out
}

type pricedLine struct {
	shop.CartLine
	name       string
	priceCents int64
	// readStock is the stock seen by the racy, unlocked read.
	readStock int
	// oversoldAfter is units_sold - units_received right after this
	// checkout's own increment (exact: the increment is row-locked).
	oversoldAfter int64
}

// Checkout turns cart lines into an order and decrements stock.
func (s *Store) Checkout(ctx context.Context, userID int64, lines []shop.CartLine) (shop.Order, error) {
	lines = NormalizeLines(lines)
	if len(lines) == 0 {
		return shop.Order{}, shop.ErrEmptyCart
	}
	var (
		order  shop.Order
		priced []pricedLine
	)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		if s.opts.FixRace {
			priced, err = reserveAtomic(ctx, tx, lines)
		} else {
			priced, err = reserveRacy(ctx, tx, lines)
		}
		if err != nil {
			return err
		}
		order, err = insertOrder(ctx, tx, userID, priced)
		if err != nil {
			return err
		}
		if !s.opts.FixRace {
			// Planted bug, part 2: write back the stock computed from the
			// stale read. units_sold is incremented atomically, which is
			// how the oversell is detected afterwards.
			for i := range priced {
				if err := writeStockRacy(ctx, tx, &priced[i]); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return shop.Order{}, err
	}
	// Committed: report any units sold beyond inventory.
	for _, l := range priced {
		if over := min(int64(l.Qty), l.oversoldAfter); over > 0 {
			s.opts.OnOversell(l.ProductID, over)
		}
	}
	return order, nil
}

// reserveRacy is the planted bug, part 1: read stock without a lock and check
// it in Go. Between this read and the write in writeStockRacy the
// transaction computes totals and inserts the order, so concurrent
// checkouts all see the same stock and all succeed.
func reserveRacy(ctx context.Context, tx pgx.Tx, lines []shop.CartLine) ([]pricedLine, error) {
	out := make([]pricedLine, 0, len(lines))
	for _, l := range lines {
		pl := pricedLine{CartLine: l}
		var stock int
		err := tx.QueryRow(ctx, "SELECT name, price_cents, stock FROM products WHERE id = $1", l.ProductID).
			Scan(&pl.name, &pl.priceCents, &stock)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &shop.OutOfStockError{ProductID: l.ProductID, Requested: l.Qty}
		}
		if err != nil {
			return nil, fmt.Errorf("read product %d: %w", l.ProductID, err)
		}
		if stock < l.Qty {
			return nil, &shop.OutOfStockError{ProductID: l.ProductID, Requested: l.Qty, Available: max(stock, 0)}
		}
		// Remember what we read; the new stock is computed from it later.
		pl.readStock = stock
		out = append(out, pl)
	}
	return out, nil
}

func writeStockRacy(ctx context.Context, tx pgx.Tx, pl *pricedLine) error {
	newStock := pl.readStock - pl.Qty // computed from the stale read
	err := tx.QueryRow(ctx, `
		UPDATE products SET stock = $2, units_sold = units_sold + $3
		WHERE id = $1
		RETURNING units_sold - units_received`, pl.ProductID, newStock, pl.Qty,
	).Scan(&pl.oversoldAfter)
	if err != nil {
		return fmt.Errorf("update stock %d: %w", pl.ProductID, err)
	}
	return nil
}

// reserveAtomic is the fix: a conditional decrement that only succeeds when
// enough stock remains, evaluated by Postgres under the row lock.
func reserveAtomic(ctx context.Context, tx pgx.Tx, lines []shop.CartLine) ([]pricedLine, error) {
	out := make([]pricedLine, 0, len(lines))
	for _, l := range lines {
		pl := pricedLine{CartLine: l}
		err := tx.QueryRow(ctx, `
			UPDATE products SET stock = stock - $2, units_sold = units_sold + $2
			WHERE id = $1 AND stock >= $2
			RETURNING name, price_cents, units_sold - units_received`, l.ProductID, l.Qty,
		).Scan(&pl.name, &pl.priceCents, &pl.oversoldAfter)
		if errors.Is(err, pgx.ErrNoRows) {
			var stock int
			if err := tx.QueryRow(ctx, "SELECT stock FROM products WHERE id = $1", l.ProductID).Scan(&stock); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("read product %d: %w", l.ProductID, err)
			}
			return nil, &shop.OutOfStockError{ProductID: l.ProductID, Requested: l.Qty, Available: max(stock, 0)}
		}
		if err != nil {
			return nil, fmt.Errorf("reserve product %d: %w", l.ProductID, err)
		}
		out = append(out, pl)
	}
	return out, nil
}

func insertOrder(ctx context.Context, tx pgx.Tx, userID int64, lines []pricedLine) (shop.Order, error) {
	var sub int64
	items := make([]shop.OrderItem, 0, len(lines))
	count := 0
	for _, l := range lines {
		line := l.priceCents * int64(l.Qty)
		sub += line
		count += l.Qty
		items = append(items, shop.OrderItem{
			ProductID: l.ProductID, Name: l.name, Qty: l.Qty,
			UnitPriceCents: l.priceCents, LineTotalCents: line,
		})
	}
	tax, ship, total := shop.Totals(sub)
	o := shop.Order{
		Status: "placed", SubtotalCents: sub, TaxCents: tax, ShippingCents: ship,
		TotalCents: total, Total: shop.Dollars(total), ItemCount: count, Items: items,
	}
	err := tx.QueryRow(ctx, `
		INSERT INTO orders (user_id, status, subtotal_cents, tax_cents, shipping_cents, total_cents, shipping_address)
		VALUES ($1, $2, $3, $4, $5, $6, (SELECT name || ', 1 Demo Street, Testville' FROM users WHERE id = $1))
		RETURNING id, created_at`, userID, o.Status, sub, tax, ship, total,
	).Scan(&o.ID, &o.CreatedAt)
	if err != nil {
		return o, fmt.Errorf("insert order: %w", err)
	}
	batch := &pgx.Batch{}
	for _, it := range items {
		batch.Queue("INSERT INTO order_items (order_id, product_id, qty, unit_price_cents) VALUES ($1, $2, $3, $4)",
			o.ID, it.ProductID, it.Qty, it.UnitPriceCents)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return o, fmt.Errorf("insert order items: %w", err)
	}
	return o, nil
}

const orderCols = `o.id, o.status, o.subtotal_cents, o.tax_cents, o.shipping_cents, o.total_cents, o.created_at,
	(SELECT coalesce(sum(qty), 0) FROM order_items oi WHERE oi.order_id = o.id)`

func scanOrder(r pgx.Row, o *shop.Order) error {
	err := r.Scan(&o.ID, &o.Status, &o.SubtotalCents, &o.TaxCents, &o.ShippingCents, &o.TotalCents, &o.CreatedAt, &o.ItemCount)
	o.Total = shop.Dollars(o.TotalCents)
	return err
}

// ListOrders returns the user's orders, newest first. Both statements filter
// on user_id and the list orders by created_at, which is exactly what the
// (user_id, created_at) index serves; without it each request scans the whole
// orders table twice.
func (s *Store) ListOrders(ctx context.Context, userID int64, page, perPage int) (shop.OrderPage, error) {
	page, perPage = shop.Clamp(page, perPage, DefaultPerPage, MaxPerPage)
	out := shop.OrderPage{Page: page, PerPage: perPage, Orders: []shop.Order{}}
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM orders WHERE user_id = $1", userID).Scan(&out.Total); err != nil {
		return out, fmt.Errorf("count orders: %w", err)
	}
	if out.Total == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, "SELECT "+orderCols+`
		FROM orders o WHERE o.user_id = $1
		ORDER BY o.created_at DESC
		LIMIT $2 OFFSET $3`, userID, perPage, (page-1)*perPage)
	if err != nil {
		return out, fmt.Errorf("list orders: %w", err)
	}
	out.Orders, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (shop.Order, error) {
		var o shop.Order
		return o, scanOrder(r, &o)
	})
	if err != nil {
		return out, fmt.Errorf("list orders: %w", err)
	}
	return out, nil
}

// Order returns one of the user's orders with its items. Orders belonging to
// other users are reported as not found.
func (s *Store) Order(ctx context.Context, userID, orderID int64) (shop.Order, error) {
	var o shop.Order
	err := scanOrder(s.pool.QueryRow(ctx, "SELECT "+orderCols+" FROM orders o WHERE o.id = $1 AND o.user_id = $2", orderID, userID), &o)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, shop.ErrNotFound
	}
	if err != nil {
		return o, fmt.Errorf("order %d: %w", orderID, err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT oi.product_id, p.name, oi.qty, oi.unit_price_cents, oi.qty * oi.unit_price_cents
		FROM order_items oi JOIN products p ON p.id = oi.product_id
		WHERE oi.order_id = $1 ORDER BY oi.product_id`, orderID)
	if err != nil {
		return o, fmt.Errorf("order %d items: %w", orderID, err)
	}
	o.Items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (shop.OrderItem, error) {
		var it shop.OrderItem
		return it, r.Scan(&it.ProductID, &it.Name, &it.Qty, &it.UnitPriceCents, &it.LineTotalCents)
	})
	return o, err
}

// Oversold lists products whose inventory ledger is inconsistent.
func (s *Store) Oversold(ctx context.Context) ([]shop.OversoldProduct, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, stock, units_received, units_sold
		FROM products
		WHERE units_sold > units_received OR stock <> units_received - units_sold
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("oversold: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (shop.OversoldProduct, error) {
		var p shop.OversoldProduct
		err := r.Scan(&p.ID, &p.Name, &p.Stock, &p.UnitsReceived, &p.UnitsSold)
		p.TrueStock = p.UnitsReceived - p.UnitsSold
		p.OversoldUnits = max(0, p.UnitsSold-p.UnitsReceived)
		p.LostUpdates = p.Stock - p.TrueStock
		return p, err
	})
}

// ResetLowStock restores products 1..LowStockProducts to LowStockQty units
// with a consistent ledger.
func (s *Store) ResetLowStock(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE products SET stock = $2, units_received = units_sold + $2
		WHERE id <= $1`, shop.LowStockProducts, shop.LowStockQty)
	if err != nil {
		return 0, fmt.Errorf("reset stock: %w", err)
	}
	return tag.RowsAffected(), nil
}
