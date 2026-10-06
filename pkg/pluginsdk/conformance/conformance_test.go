package conformance_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ivan825/Stampede/internal/pluginhost/plugintest"
	"github.com/Ivan825/Stampede/pkg/pluginsdk/conformance"
)

func TestMain(m *testing.M) {
	code := m.Run()
	plugintest.Remove()
	os.Exit(code)
}

// TestEchoConforms runs the suite against Stampede's test plugin.
func TestEchoConforms(t *testing.T) {
	bin := filepath.Join(plugintest.EchoDir(t), "stampede-plugin-echo")
	conformance.Run(t, bin, conformance.Options{
		Cases: []conformance.Case{
			{Step: "say", Config: map[string]any{"text": "hello", "count": 2}},
			{Step: "connect", Config: map[string]any{}},
			{Step: "say", Config: map[string]any{"text": "no", "fail": "echo refused"}, WantClass: "echo refused"},
			{Step: "panic", Config: map[string]any{}, WantClass: "plugin panic"},
		},
		VUs:          40,
		SkipBadInput: []string{"crash"},
	})
}
