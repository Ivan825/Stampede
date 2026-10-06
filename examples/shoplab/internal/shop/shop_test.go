package shop

import (
	"errors"
	"testing"
)

func TestTotals(t *testing.T) {
	cases := []struct {
		sub, tax, ship, total int64
	}{
		{0, 0, 0, 0},
		{1000, 83, 499, 1582}, // 82.5 rounds half-up to 83
		{4999, 412, 499, 5910},
		{5000, 413, 0, 5413}, // free shipping threshold
		{12345, 1018, 0, 13363},
	}
	for _, c := range cases {
		tax, ship, total := Totals(c.sub)
		if tax != c.tax || ship != c.ship || total != c.total {
			t.Errorf("Totals(%d) = %d,%d,%d want %d,%d,%d", c.sub, tax, ship, total, c.tax, c.ship, c.total)
		}
	}
}

func TestDollars(t *testing.T) {
	if got := Dollars(1999); got != 19.99 {
		t.Fatalf("Dollars(1999) = %v", got)
	}
	if got := Dollars(5); got != 0.05 {
		t.Fatalf("Dollars(5) = %v", got)
	}
}

func TestClamp(t *testing.T) {
	p, pp := Clamp(0, 0, 20, 100)
	if p != 1 || pp != 20 {
		t.Fatalf("got %d %d", p, pp)
	}
	p, pp = Clamp(3, 500, 20, 100)
	if p != 3 || pp != 100 {
		t.Fatalf("got %d %d", p, pp)
	}
}

func TestOutOfStockError(t *testing.T) {
	var err error = &OutOfStockError{ProductID: 7, Requested: 3, Available: 1}
	var oos *OutOfStockError
	if !errors.As(err, &oos) || oos.ProductID != 7 {
		t.Fatal("errors.As failed")
	}
	if err.Error() == "" {
		t.Fatal("empty message")
	}
}
