package coordinator_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/pluginhost/plugintest"
	"github.com/Ivan825/Stampede/internal/worker"
)

func TestMain(m *testing.M) {
	code := m.Run()
	plugintest.Remove()
	os.Exit(code)
}

const pluginScenario = `
metadata: {name: plugins}
journeys: [{name: a, steps: [{name: say, plugin: echo.say, with: {text: "hi ${vu}"}}]}]
load: {vus: 2, iterations: 20}`

// A worker without a plugin the scenario needs refuses the run before
// any load starts, naming the plugin.
func TestWorkerWithoutPluginRefusesTheRun(t *testing.T) {
	c := newCluster(t, coordinator.Config{})
	c.addWorker(worker.Config{Name: "has-echo", PluginDir: plugintest.EchoDir(t)})
	c.addWorker(worker.Config{Name: "bare", PluginDir: t.TempDir()})
	c.waitConnected(2, 5*time.Second)

	for _, w := range c.coord.Workers() {
		has := slices.Contains(w.Capacity.Protocols, "plugin:echo")
		if has != (w.Name == "has-echo") {
			t.Errorf("worker %s advertises %v", w.Name, w.Capacity.Protocols)
		}
	}
	_, err := c.coord.Start(context.Background(), coordinator.RunSpec{ID: "p1", Scenario: []byte(pluginScenario), StartDelay: 2 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "worker bare refused the run") || !strings.Contains(err.Error(), "stampede plugin install echo") {
		t.Fatalf("got %v", err)
	}
}

func TestDistributedPluginRun(t *testing.T) {
	c := newCluster(t, coordinator.Config{})
	dir := plugintest.EchoDir(t)
	c.addWorker(worker.Config{Name: "a", PluginDir: dir})
	c.addWorker(worker.Config{Name: "b", PluginDir: dir})
	c.waitConnected(2, 5*time.Second)
	r, err := c.coord.Start(context.Background(), coordinator.RunSpec{ID: "p2", Scenario: []byte(pluginScenario), StartDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	out := collect(t, r, 20*time.Second)
	if out.err != nil {
		t.Fatal(out.err)
	}
	tot := metrics.NewSnapshot(0)
	for _, s := range out.res.Snapshots {
		tot.Merge(s)
	}
	st := tot.Steps[0]
	if st == nil || st.Requests != 20 || st.Failed != 0 || st.Protocols["echo"] != 20 {
		t.Fatalf("plugin step across two workers: %+v", st)
	}
}
