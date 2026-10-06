package seed

import (
	"bytes"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/examples/shoplab/internal/shop"
)

func TestUserEmail(t *testing.T) {
	if UserEmail(1) != "user0001@shoplab.test" || UserEmail(1000) != "user1000@shoplab.test" {
		t.Fatal(UserEmail(1), UserEmail(1000))
	}
}

func TestWriteUsersCSV(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteUsersCSV(&buf, 3); err != nil {
		t.Fatal(err)
	}
	want := "email,password\nuser0001@shoplab.test,shoplab-pass\nuser0002@shoplab.test,shoplab-pass\nuser0003@shoplab.test,shoplab-pass\n"
	if buf.String() != want {
		t.Fatalf("got %q", buf.String())
	}
}

// The committed data feeder must match what the generator produces.
func TestCommittedUsersCSV(t *testing.T) {
	got, err := os.ReadFile(filepath.Join("..", "..", "data", "users.csv"))
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := WriteUsersCSV(&want, 1000); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatal("data/users.csv is stale; regenerate with: go run ./cmd/shoplab users-csv --users 1000 > data/users.csv")
	}
}

func TestGenerationIsDeterministic(t *testing.T) {
	gen := func() ([]product, []order) {
		rng := rand.New(rand.NewPCG(rngSeed1, rngSeed2))
		ps := genProducts(rng, 200)
		return ps, genOrders(rng, ps, 20, 5)
	}
	p1, o1 := gen()
	p2, o2 := gen()
	for i := range p1 {
		if p1[i] != p2[i] {
			t.Fatalf("product %d differs", i)
		}
	}
	if len(o1) != len(o2) || len(o1) != 100 {
		t.Fatalf("orders %d vs %d", len(o1), len(o2))
	}
	for i := range o1 {
		if o1[i].userID != o2[i].userID || !o1[i].created.Equal(o2[i].created) || o1[i].sub != o2[i].sub {
			t.Fatalf("order %d differs", i)
		}
	}
}

func TestLowStockAndLedger(t *testing.T) {
	rng := rand.New(rand.NewPCG(rngSeed1, rngSeed2))
	ps := genProducts(rng, 500)
	orders := genOrders(rng, ps, 50, 10)
	for _, p := range ps[:shop.LowStockProducts] {
		if p.stock != shop.LowStockQty || p.sold != 0 {
			t.Fatalf("low-stock product %d: stock=%d sold=%d", p.id, p.stock, p.sold)
		}
	}
	var sold int64
	for _, p := range ps {
		sold += p.sold
	}
	var items int64
	for i, o := range orders {
		if i > 0 && o.created.Before(orders[i-1].created) {
			t.Fatal("orders not sorted by time")
		}
		seen := map[int64]bool{}
		for _, it := range o.items {
			if it.productID <= shop.LowStockProducts {
				t.Fatal("historic order touches a low-stock product")
			}
			if seen[it.productID] {
				t.Fatal("duplicate product in order")
			}
			seen[it.productID] = true
			items += int64(it.qty)
		}
	}
	if sold != items {
		t.Fatalf("ledger mismatch: sold %d, items %d", sold, items)
	}
}

func TestProductNamesSearchable(t *testing.T) {
	rng := rand.New(rand.NewPCG(rngSeed1, rngSeed2))
	shoes := 0
	for _, p := range genProducts(rng, 10000) {
		if strings.Contains(strings.ToLower(p.name), "shoe") {
			shoes++
		}
	}
	if shoes < 100 {
		t.Fatalf("only %d products match q=shoe", shoes)
	}
}

func TestHotProducts(t *testing.T) {
	if HotProducts(10000) != 100 || HotProducts(200) != 10 || HotProducts(5) != 5 {
		t.Fatal(HotProducts(10000), HotProducts(200), HotProducts(5))
	}
}
