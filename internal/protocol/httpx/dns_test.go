package httpx

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestDNSCacheSharesLookups(t *testing.T) {
	c := NewDNSCache(time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := c.Lookup(context.Background(), "localhost")
			if err != nil || !a.IsLoopback() {
				t.Errorf("lookup: %v %v", a, err)
			}
		}()
	}
	wg.Wait()
	if len(c.entries) != 1 {
		t.Errorf("entries %d", len(c.entries))
	}
	if a, _ := c.Lookup(context.Background(), "10.1.2.3"); a.String() != "10.1.2.3" {
		t.Error("IP literals bypass the cache")
	}
	if _, err := c.Lookup(context.Background(), "no-such-host.invalid"); err == nil {
		t.Error("expected lookup failure")
	}
}
