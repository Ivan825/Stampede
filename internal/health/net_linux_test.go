package health

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCountEphemeral(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tcp")
	// Local ports 0x1F90 (8080, listening), 0x8000 (32768), 0xEE47
	// (60999), 0xEE48 (61000, outside) and 0x9C40 (40000, TIME_WAIT).
	data := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0000000000000000 100 0 0 10 0
   1: 0100007F:8000 0100007F:1F90 01 00000000:00000000 00:00000000 00000000     0        0 2 1 0000000000000000 20 4 30 10 -1
   2: 0100007F:EE47 0100007F:1F90 01 00000000:00000000 00:00000000 00000000     0        0 3 1 0000000000000000 20 4 30 10 -1
   3: 0100007F:EE48 0100007F:1F90 01 00000000:00000000 00:00000000 00000000     0        0 4 1 0000000000000000 20 4 30 10 -1
   4: 0100007F:9C40 0100007F:1F90 06 00000000:00000000 03:00000000 00000000     0        0 0 3 0000000000000000
`
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := countEphemeral(p, 32768, 60999)
	if err != nil || n != 3 {
		t.Fatalf("counted %d (%v), want 3", n, err)
	}
	if _, total, ok := portUsage(); !ok || total == 0 {
		t.Errorf("portUsage on this machine: total %d ok %v", total, ok)
	}
}
