// Package shop holds ShopLab's domain types, errors and the storage
// interfaces the HTTP layer depends on. Keeping these here lets handlers be
// unit-tested with in-memory fakes.
package shop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// Sentinel errors mapped to HTTP status codes by the API layer.
var (
	ErrNotFound           = errors.New("not found")
	ErrEmptyCart          = errors.New("cart is empty")
	ErrInvalidCredentials = errors.New("invalid email or password")
)

// OutOfStockError is returned by checkout when a cart line cannot be filled.
type OutOfStockError struct {
	ProductID int64
	Requested int
	Available int
}

func (e *OutOfStockError) Error() string {
	return fmt.Sprintf("product %d is out of stock (requested %d, available %d)", e.ProductID, e.Requested, e.Available)
}

// Category is a product category.
type Category struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// ProductSummary is one row of the product listing.
type ProductSummary struct {
	ID            int64    `json:"id"`
	SKU           string   `json:"sku"`
	Name          string   `json:"name"`
	PriceCents    int64    `json:"priceCents"`
	Price         float64  `json:"price"`
	Stock         int      `json:"stock"`
	Category      Category `json:"category"`
	AverageRating float64  `json:"averageRating"`
	ReviewCount   int64    `json:"reviewCount"`
}

// ProductPage is a page of the product listing.
type ProductPage struct {
	Page     int              `json:"page"`
	PerPage  int              `json:"perPage"`
	Total    int64            `json:"total"`
	Products []ProductSummary `json:"products"`
}

// ListParams filters and paginates the product listing.
type ListParams struct {
	Page     int
	PerPage  int
	Query    string // case-insensitive substring of the product name
	Category string // category slug, or numeric category id
}

// Review is a single product review.
type Review struct {
	ID        int64     `json:"id"`
	Rating    int       `json:"rating"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"createdAt"`
}

// TermCount is a frequently used word across a product's reviews.
type TermCount struct {
	Term  string `json:"term"`
	Count int64  `json:"count"`
}

// ReviewSummary aggregates all reviews of one product. Computing it is the
// expensive part of the product detail page.
type ReviewSummary struct {
	AverageRating float64          `json:"averageRating"`
	Count         int64            `json:"count"`
	Histogram     map[string]int64 `json:"histogram"`
	Highlights    []TermCount      `json:"highlights"`
	Latest        []Review         `json:"latest"`
}

// ProductDetail is the full product page.
type ProductDetail struct {
	ID          int64         `json:"id"`
	SKU         string        `json:"sku"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	PriceCents  int64         `json:"priceCents"`
	Price       float64       `json:"price"`
	Stock       int           `json:"stock"`
	Category    Category      `json:"category"`
	Reviews     ReviewSummary `json:"reviews"`
	GeneratedAt time.Time     `json:"generatedAt"`
}

// ProductBrief is the minimal product data the cart needs.
type ProductBrief struct {
	ID         int64
	Name       string
	PriceCents int64
	Stock      int
}

// User is a registered shopper.
type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
}

// CartLine is one product + quantity in a cart.
type CartLine struct {
	ProductID int64 `json:"productId"`
	Qty       int   `json:"qty"`
}

// CartItem is a cart line enriched with product data.
type CartItem struct {
	ProductID      int64   `json:"productId"`
	Name           string  `json:"name"`
	Qty            int     `json:"qty"`
	PriceCents     int64   `json:"priceCents"`
	LineTotalCents int64   `json:"lineTotalCents"`
	LineTotal      float64 `json:"lineTotal"`
}

// CartView is the cart as returned to clients.
type CartView struct {
	Items         []CartItem `json:"items"`
	ItemCount     int        `json:"itemCount"`
	SubtotalCents int64      `json:"subtotalCents"`
	Subtotal      float64    `json:"subtotal"`
}

// OrderItem is a line of a placed order.
type OrderItem struct {
	ProductID      int64  `json:"productId"`
	Name           string `json:"name"`
	Qty            int    `json:"qty"`
	UnitPriceCents int64  `json:"unitPriceCents"`
	LineTotalCents int64  `json:"lineTotalCents"`
}

// Order is a placed order.
type Order struct {
	ID            int64       `json:"id"`
	Status        string      `json:"status"`
	SubtotalCents int64       `json:"subtotalCents"`
	TaxCents      int64       `json:"taxCents"`
	ShippingCents int64       `json:"shippingCents"`
	TotalCents    int64       `json:"totalCents"`
	Total         float64     `json:"total"`
	ItemCount     int         `json:"itemCount"`
	CreatedAt     time.Time   `json:"createdAt"`
	Items         []OrderItem `json:"items,omitempty"`
}

// OrderPage is a page of a user's order history.
type OrderPage struct {
	Page    int     `json:"page"`
	PerPage int     `json:"perPage"`
	Total   int64   `json:"total"`
	Orders  []Order `json:"orders"`
}

// OversoldProduct describes a product whose inventory ledger is inconsistent:
// more units were sold than were ever received, and/or the stock column
// drifted from units_received - units_sold because of lost updates.
type OversoldProduct struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Stock         int64  `json:"stock"`
	UnitsReceived int64  `json:"unitsReceived"`
	UnitsSold     int64  `json:"unitsSold"`
	TrueStock     int64  `json:"trueStock"`
	OversoldUnits int64  `json:"oversoldUnits"`
	LostUpdates   int64  `json:"lostUpdates"`
}

// Catalog reads products.
type Catalog interface {
	ListProducts(ctx context.Context, p ListParams) (ProductPage, error)
	// ProductDetail computes the full product page straight from the
	// database (uncached; this is the expensive call).
	ProductDetail(ctx context.Context, id int64) (ProductDetail, error)
	ProductBriefs(ctx context.Context, ids []int64) (map[int64]ProductBrief, error)
}

// Users reads shoppers.
type Users interface {
	UserByEmail(ctx context.Context, email string) (User, error)
	UserByID(ctx context.Context, id int64) (User, error)
}

// Orders places and reads orders.
type Orders interface {
	Checkout(ctx context.Context, userID int64, lines []CartLine) (Order, error)
	ListOrders(ctx context.Context, userID int64, page, perPage int) (OrderPage, error)
	Order(ctx context.Context, userID, orderID int64) (Order, error)
}

// Inventory exposes the oversell diagnostics.
type Inventory interface {
	Oversold(ctx context.Context) ([]OversoldProduct, error)
	// ResetLowStock restores the demo low-stock products to their initial
	// stock and a consistent ledger, so the race demo can be repeated.
	ResetLowStock(ctx context.Context) (int64, error)
}

// Carts stores carts keyed by an opaque cart id (the session id).
type Carts interface {
	SetQty(ctx context.Context, cartID string, productID int64, qty int) error
	Remove(ctx context.Context, cartID string, productID int64) error
	Lines(ctx context.Context, cartID string) ([]CartLine, error)
	Clear(ctx context.Context, cartID string) error
}

// Low-stock demo products: ids 1..LowStockProducts start with LowStockQty units.
const (
	LowStockProducts = 10
	LowStockQty      = 5
)

// Pricing constants for checkout totals.
const (
	TaxRateBasisPoints   = 825  // 8.25 %
	FreeShippingMinCents = 5000 // orders of $50 or more ship free
	ShippingCents        = 499
)

// Totals computes tax, shipping and grand total for a subtotal. Tax is
// rounded half-up to the cent.
func Totals(subtotalCents int64) (tax, shipping, total int64) {
	tax = (subtotalCents*TaxRateBasisPoints + 5000) / 10000
	if subtotalCents > 0 && subtotalCents < FreeShippingMinCents {
		shipping = ShippingCents
	}
	return tax, shipping, subtotalCents + tax + shipping
}

// Dollars converts cents to a float rounded to two decimals, for display
// fields. Integer *Cents fields are authoritative.
func Dollars(cents int64) float64 {
	return math.Round(float64(cents)) / 100
}

// Clamp normalises pagination parameters.
func Clamp(page, perPage, defPerPage, maxPerPage int) (int, int) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = defPerPage
	}
	if perPage > maxPerPage {
		perPage = maxPerPage
	}
	return page, perPage
}
