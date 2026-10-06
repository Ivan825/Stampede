package store

import (
	"reflect"
	"testing"

	"github.com/Ivan825/Stampede/examples/shoplab/internal/shop"
)

func TestProductFilter(t *testing.T) {
	cases := []struct {
		p     shop.ListParams
		where string
		args  []any
	}{
		{shop.ListParams{}, "", nil},
		{shop.ListParams{Query: " shoe "}, " WHERE p.name ILIKE $1", []any{"%shoe%"}},
		{shop.ListParams{Category: "3"}, " WHERE p.category_id = $1", []any{int64(3)}},
		{shop.ListParams{Category: "Shoes"}, " WHERE p.category_id = (SELECT id FROM categories WHERE slug = $1)", []any{"shoes"}},
		{
			shop.ListParams{Query: "50%_off", Category: "books"},
			" WHERE p.name ILIKE $1 AND p.category_id = (SELECT id FROM categories WHERE slug = $2)",
			[]any{`%50\%\_off%`, "books"},
		},
	}
	for _, c := range cases {
		where, args := productFilter(c.p)
		if where != c.where || !reflect.DeepEqual(args, c.args) {
			t.Errorf("productFilter(%+v) = %q %v, want %q %v", c.p, where, args, c.where, c.args)
		}
	}
}

func TestNormalizeLines(t *testing.T) {
	got := NormalizeLines([]shop.CartLine{
		{ProductID: 9, Qty: 1},
		{ProductID: 2, Qty: 2},
		{ProductID: 9, Qty: 3},
		{ProductID: 5, Qty: 0},
		{ProductID: 4, Qty: -1},
	})
	want := []shop.CartLine{{ProductID: 2, Qty: 2}, {ProductID: 9, Qty: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if len(NormalizeLines(nil)) != 0 {
		t.Fatal("nil lines should normalise to empty")
	}
}

func TestRound2(t *testing.T) {
	if round2(4.4567) != 4.46 || round2(0) != 0 || round2(3.333333) != 3.33 {
		t.Fatal("round2")
	}
}
