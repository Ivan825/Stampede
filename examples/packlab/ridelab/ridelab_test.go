package ridelab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

func start(t *testing.T, fixes string) (*server, string) {
	s, h := newServer(labkit.Config{Fast: true, Fixes: labkit.ParseFixes(fixes)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return s, srv.URL
}

func call(t *testing.T, method, u, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func login(t *testing.T, base, id string) string {
	t.Helper()
	code, m := call(t, "POST", base+"/api/login", "", fmt.Sprintf(`{"id":%q,"password":%q}`, id, Password))
	if code != 200 {
		t.Fatalf("login %s: %d %v", id, code, m)
	}
	return m["token"].(string)
}

const ride = `{"type":"ride","pickup":{"lat":12.97,"lng":77.59},"dropoff":{"lat":12.985,"lng":77.605}}`

func request(t *testing.T, base, tok, body string) string {
	t.Helper()
	code, m := call(t, "POST", base+"/api/trips", tok, body)
	if code != 201 || m["status"] != "searching" {
		t.Fatalf("request: %d %v", code, m)
	}
	return m["tripId"].(string)
}

func waitAssigned(t *testing.T, base, tok, id string) map[string]any {
	t.Helper()
	for range 200 {
		_, m := call(t, "GET", base+"/api/trips/"+id, tok, "")
		if m["status"] != "searching" {
			return m
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("never matched")
	return nil
}

func TestRideTrackedToCompletion(t *testing.T) {
	_, base := start(t, "")
	tok := login(t, base, "r0001")
	id := request(t, base, tok, ride)
	if m := waitAssigned(t, base, tok, id); m["driver"] == nil {
		t.Fatalf("assigned: %v", m)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/api/trips/"+id+"/track",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	var statuses []string
	locations := 0
	for len(statuses) == 0 || statuses[len(statuses)-1] != "completed" {
		_, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("after %v: %v", statuses, err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		switch m["type"] {
		case "status":
			statuses = append(statuses, m["status"].(string))
		case "driver_location":
			if m["etaSeconds"] == nil {
				t.Fatalf("location without an ETA: %v", m)
			}
			locations++
		}
	}
	if !reflect.DeepEqual(statuses, []string{"driver_assigned", "arrived", "in_progress", "completed"}) || locations == 0 {
		t.Fatalf("statuses %v, %d locations", statuses, locations)
	}
	if code, _ := call(t, "POST", base+"/api/trips/"+id+"/rating", tok, `{"stars":5}`); code != 200 {
		t.Fatalf("rating: %d", code)
	}
	if code, _ := call(t, "GET", base+"/api/trips/"+id, login(t, base, "r0002"), ""); code != 404 {
		t.Fatalf("someone else's trip: %d", code)
	}
}

func TestFoodOrder(t *testing.T) {
	s, base := start(t, "")
	tok := login(t, base, "r0003")
	_, rs := call(t, "GET", base+"/api/restaurants?lat=12.97&lng=77.59", tok, "")
	rest := rs["restaurants"].([]any)[0].(map[string]any)
	body := fmt.Sprintf(`{"type":"food","restaurantId":%q,"items":[{"id":"x","qty":2}],"dropoff":{"lat":12.97,"lng":77.59}}`, rest["id"])
	id := request(t, base, tok, body)
	m := waitAssigned(t, base, tok, id)
	loc := rest["location"].(map[string]any)
	if pk := m["pickup"].(map[string]any); pk["lat"] != loc["lat"] {
		t.Fatalf("the pickup is not the restaurant: %v vs %v", pk, loc)
	}
	if s.trips[id].Restaurant != rest["id"] {
		t.Fatal("restaurant not recorded")
	}
}

func TestGeoBottleneck(t *testing.T) {
	var found [][]string
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, fixes := range []string{"", "geo"} {
		s, _ := start(t, fixes)
		ns := s.nearby(point{12.97, 77.59}, 1500, at)
		var ids []string
		for _, n := range ns {
			ids = append(ids, n.d.id)
		}
		found = append(found, ids)
		got := s.examined.Load()
		if fixes == "" && got != Drivers {
			t.Errorf("without the fix %d drivers checked, want all %d", got, Drivers)
		}
		if fixes != "" && got > Drivers/4 {
			t.Errorf("with the fix %d drivers checked", got)
		}
	}
	if len(found[0]) == 0 || !reflect.DeepEqual(found[0], found[1]) {
		t.Fatalf("the fix changed who is nearby: %d vs %d", len(found[0]), len(found[1]))
	}
}

func TestDispatchBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "dispatch"} {
		s, base := start(t, fixes)
		tok := login(t, base, "r0004")
		waitAssigned(t, base, tok, request(t, base, tok, ride))
		n := s.locked.Load()
		if fixes == "" && n == 0 {
			t.Error("without the fix no route was searched under the fleet lock")
		}
		if fixes != "" && n != 0 {
			t.Errorf("with the fix %d routes were searched under the fleet lock", n)
		}
	}
}

func TestETABottleneck(t *testing.T) {
	for _, fixes := range []string{"", "eta"} {
		s, base := start(t, fixes)
		tok := login(t, base, "r0005")
		for range 10 {
			if code, m := call(t, "GET", base+"/api/eta?from=12.97,77.59&to=12.99,77.61", tok, ""); code != 200 || m["seconds"].(float64) <= 0 {
				t.Fatalf("eta: %d %v", code, m)
			}
		}
		want := int64(10)
		if fixes != "" {
			want = 1
		}
		if n := s.routed.Load(); n != want {
			t.Errorf("fixes %q: %d route searches for ten ETAs, want %d", fixes, n, want)
		}
	}
}

func TestTrackingETABottleneck(t *testing.T) {
	for _, fixes := range []string{"", "eta"} {
		s, base := start(t, fixes)
		tok := login(t, base, "r0007")
		id := request(t, base, tok, ride)
		waitAssigned(t, base, tok, id)
		before := s.routed.Load()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/api/trips/"+id+"/track",
			&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}})
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 5; {
			_, b, err := c.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "driver_location") {
				n++
			}
		}
		c.CloseNow()
		cancel()
		routed := s.routed.Load() - before
		if fixes == "" && routed < 5 {
			t.Errorf("without the fix five tracking updates made %d routing calls", routed)
		}
		if fixes != "" && routed != 0 {
			t.Errorf("with the fix tracking made %d routing calls", routed)
		}
	}
}

func TestDriverStream(t *testing.T) {
	_, base := start(t, "")
	tok := login(t, base, "d00042")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/api/drivers/stream",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"location","lat":12.9501,"lng":77.5501}`))
	_, b, err := c.Read(ctx)
	if err != nil || !strings.Contains(string(b), `"type":"ack"`) {
		t.Fatalf("ack: %v %s", err, b)
	}
	_, m := call(t, "GET", base+"/api/drivers/nearby?lat=12.95&lng=77.55", login(t, base, "r0006"), "")
	if !strings.Contains(fmt.Sprint(m["cars"]), "d00042") {
		t.Fatalf("the streaming driver is not on the map: %v", m)
	}
	if code, _ := call(t, "POST", base+"/api/trips", tok, ride); code != 403 {
		t.Fatalf("a driver requested a trip: %d", code)
	}
}
