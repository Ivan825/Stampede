package pdf

import (
	"bytes"
	"context"
	"testing"

	"github.com/Ivan825/Stampede/internal/engine"
	"github.com/Ivan825/Stampede/internal/report"
)

func TestWrite(t *testing.T) {
	if _, err := engine.FindChrome(); err != nil {
		t.Skip("no Chrome or Chromium:", err)
	}
	r := &report.Report{FormatVersion: 1, Scenario: "pdf-test", Target: "http://localhost", Verdict: report.VerdictPass}
	var buf bytes.Buffer
	if err := Write(context.Background(), r, &buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) || buf.Len() < 1000 {
		t.Fatalf("not a PDF: %d bytes starting %q", buf.Len(), buf.Bytes()[:min(buf.Len(), 16)])
	}
}
