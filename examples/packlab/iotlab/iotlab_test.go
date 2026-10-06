package iotlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

type lab struct {
	s      *server
	base   string
	broker string
}

func start(t *testing.T, cfg labkit.Config) *lab {
	t.Helper()
	cfg.Listen = "127.0.0.1:0"
	s, app, err := newServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return &lab{s: s, base: srv.URL, broker: app.Env["MQTT_BROKER"]}
}

// thing is a connected device collecting what arrives on its topics.
type thing struct {
	t      *testing.T
	id     string
	client paho.Client
	got    chan paho.Message
}

func (l *lab) connect(t *testing.T, id, password string) (*thing, error) {
	t.Helper()
	d := &thing{t: t, id: id, got: make(chan paho.Message, 1000)}
	opts := paho.NewClientOptions().AddBroker(l.broker).SetClientID(id).SetUsername(id).SetPassword(password).
		SetAutoReconnect(false).SetConnectRetry(false).
		SetDefaultPublishHandler(func(_ paho.Client, m paho.Message) { d.got <- m })
	d.client = paho.NewClient(opts)
	if tok := d.client.Connect(); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		return nil, fmt.Errorf("connect %s: %v", id, tok.Error())
	}
	t.Cleanup(func() { d.client.Disconnect(0) })
	return d, nil
}

func (l *lab) mustConnect(t *testing.T, id string) *thing {
	t.Helper()
	d, err := l.connect(t, id, Password)
	if err != nil {
		t.Fatal(err)
	}
	if tok := d.client.Subscribe("devices/"+id+"/#", 1, nil); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatalf("subscribe: %v", tok.Error())
	}
	return d
}

func (d *thing) publish(payload string) {
	if tok := d.client.Publish("devices/"+d.id+"/telemetry", 1, false, payload); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		d.t.Fatalf("publish: %v", tok.Error())
	}
}

// expect waits for a message on one of the device's topics.
func (d *thing) expect(suffix string) map[string]any {
	d.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m := <-d.got:
			if m.Topic() != "devices/"+d.id+"/"+suffix {
				continue
			}
			var v map[string]any
			_ = json.Unmarshal(m.Payload(), &v)
			return v
		case <-timeout:
			d.t.Fatalf("%s: nothing on %s", d.id, suffix)
			return nil
		}
	}
}

func get(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return v
}

func put(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return resp.StatusCode, v
}

func TestTelemetryAndShadows(t *testing.T) {
	for _, fixes := range []string{"", "all"} {
		l := start(t, labkit.Config{Fixes: labkit.ParseFixes(fixes), Fast: true})
		d := l.mustConnect(t, "dev-7")
		d.publish(`{"seq": 1, "temp": 21.5}`)
		if ack := d.expect("acks"); ack["seq"] != 1.0 {
			t.Fatalf("fixes %q: ack %v", fixes, ack)
		}
		if r := get(t, l.base+"/api/devices/dev-7/telemetry"); len(r["readings"].([]any)) != 1 {
			t.Fatalf("telemetry %v", r)
		}
		if f := get(t, l.base+"/api/fleet"); f["online"] != 1.0 {
			t.Fatalf("fixes %q: fleet %v", fixes, f)
		}

		// A desired state set over HTTP is pushed to the connected
		// device, and the device can ask for it.
		if code, v := put(t, l.base+"/api/devices/dev-7/shadow", `{"desired": {"interval": 30}}`); code != 200 || v["pushed"] != true {
			t.Fatalf("set shadow: %d %v", code, v)
		}
		if sh := d.expect("shadow"); sh["version"] != 1.0 {
			t.Fatalf("pushed shadow %v", sh)
		}
		if tok := d.client.Publish("devices/dev-7/shadow/get", 1, false, `{"token": "t-1"}`); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
			t.Fatal(tok.Error())
		}
		if sh := d.expect("shadow"); sh["token"] != "t-1" || sh["desired"].(map[string]any)["interval"] != 30.0 {
			t.Fatalf("shadow reply %v", sh)
		}

		// A reading without a seq is refused on the ack topic.
		d.publish(`not json`)
		if ack := d.expect("acks"); ack["error"] == nil {
			t.Fatalf("bad reading acked: %v", ack)
		}
	}
}

func TestDevicesStayInTheirLane(t *testing.T) {
	l := start(t, labkit.Config{Fast: true})
	if _, err := l.connect(t, "dev-1", "wrong"); err == nil {
		t.Fatal("a wrong password connected")
	}
	if _, err := l.connect(t, "sensor-9", Password); err == nil {
		t.Fatal("an unregistered device connected")
	}
	d := l.mustConnect(t, "dev-2")
	tok := d.client.Subscribe("devices/dev-3/#", 1, nil)
	tok.WaitTimeout(5 * time.Second)
	if st, ok := tok.(*paho.SubscribeToken); !ok || st.Result()["devices/dev-3/#"] != 0x80 {
		t.Fatal("a device subscribed to another device's topics")
	}
	if code, v := put(t, l.base+"/api/devices/dev-4/shadow", `{"desired": {"on": true}}`); code != 200 || v["pushed"] != false {
		t.Fatalf("shadow of an offline device: %d %v", code, v)
	}
}

func TestIngestBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "ingest"} {
		l := start(t, labkit.Config{Fixes: labkit.ParseFixes(fixes)})
		var wg sync.WaitGroup
		for i := 1; i <= 8; i++ {
			d := l.mustConnect(t, fmt.Sprintf("dev-%d", i))
			wg.Add(1)
			go func() {
				defer wg.Done()
				for n := range 10 {
					d.publish(fmt.Sprintf(`{"seq": %d}`, n))
				}
				for range 10 {
					d.expect("acks")
				}
			}()
		}
		wg.Wait()
		peak := l.s.writing.Peak()
		if fixes == "" && peak != 1 {
			t.Errorf("without the fix readings are stored one at a time: peak %d", peak)
		}
		if fixes != "" && peak < 2 {
			t.Errorf("with the fix readings are stored in parallel: peak %d", peak)
		}
	}
}

func TestPresenceBottleneck(t *testing.T) {
	for _, tc := range []struct {
		fixes   string
		scanned int64
	}{{"", 2 * Devices}, {"presence", 0}} {
		l := start(t, labkit.Config{Fixes: labkit.ParseFixes(tc.fixes), Fast: true})
		before := l.s.scanned.Load()
		d := l.mustConnect(t, "dev-9")
		d.client.Disconnect(0)
		deadline := time.Now().Add(5 * time.Second)
		for get(t, l.base+"/api/fleet")["online"] != 0.0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if got := l.s.scanned.Load() - before; got != tc.scanned {
			t.Errorf("fixes %q: a connect and a disconnect scanned %d devices, want %d", tc.fixes, got, tc.scanned)
		}
	}
}
