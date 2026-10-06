// Package pdf prints a report to PDF with headless Chrome: the same page
// as the HTML report, in its light theme.
package pdf

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/Ivan825/Stampede/internal/engine"
	"github.com/Ivan825/Stampede/internal/report"
)

// Timeout bounds starting Chrome and printing.
const Timeout = time.Minute

// Write renders r as PDF into w. It needs Chrome or Chromium (found as for
// browser steps, or set STAMPEDE_CHROME).
func Write(ctx context.Context, r *report.Report, w io.Writer) error {
	var html bytes.Buffer
	if err := r.WriteHTML(&html); err != nil {
		return err
	}
	b, err := FromHTML(ctx, html.Bytes())
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// FromHTML prints a self-contained HTML page to PDF (A4).
func FromHTML(ctx context.Context, html []byte) ([]byte, error) {
	chrome, err := engine.FindChrome()
	if err != nil {
		return nil, fmt.Errorf("PDF export needs Chrome or Chromium: %w", err)
	}
	dir, err := os.MkdirTemp("", "stampede-pdf-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "report.html")
	if err := os.WriteFile(file, html, 0o600); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome))
	if os.Getenv("STAMPEDE_CHROME_NO_SANDBOX") == "true" {
		opts = append(opts, chromedp.NoSandbox)
	}
	actx, stopA := chromedp.NewExecAllocator(ctx, opts...)
	defer stopA()
	tab, stopT := chromedp.NewContext(actx)
	defer stopT()

	var out []byte
	err = chromedp.Run(tab,
		emulation.SetEmulatedMedia().WithMedia("print").WithFeatures([]*emulation.MediaFeature{
			{Name: "prefers-color-scheme", Value: "light"},
		}),
		chromedp.Navigate("file://"+filepath.ToSlash(file)),
		chromedp.ActionFunc(func(ctx context.Context) error {
			// A4 with 12 mm margins.
			b, _, err := page.PrintToPDF().
				WithPrintBackground(true).
				WithPaperWidth(8.27).WithPaperHeight(11.69).
				WithMarginTop(0.47).WithMarginBottom(0.47).WithMarginLeft(0.47).WithMarginRight(0.47).
				WithGenerateDocumentOutline(true).
				Do(ctx)
			out = b
			return err
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("printing the report to PDF: %w", err)
	}
	return out, nil
}
