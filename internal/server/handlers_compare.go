package server

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// maxCompareRuns bounds the runs on each side of a comparison.
const maxCompareRuns = 20

// CompareRuns compares finished runs of version A with runs of version B,
// as stampede compare does with saved reports.
func (h *handlers) CompareRuns(ctx context.Context, req gen.CompareRunsRequestObject) (gen.CompareRunsResponseObject, error) {
	p, err := need(ctx, auth.PermView)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil {
		return nil, errInvalid("give the runs to compare as a and b")
	}
	for side, ids := range map[string][]uuid.UUID{"a": b.A, "b": b.B} {
		if len(ids) == 0 || len(ids) > maxCompareRuns {
			return nil, errInvalid(fmt.Sprintf("%s must list 1 to %d runs", side, maxCompareRuns))
		}
	}
	labelA, labelB := "A", "B"
	for _, l := range []struct {
		in  *string
		out *string
	}{{b.LabelA, &labelA}, {b.LabelB, &labelB}} {
		if l.in == nil {
			continue
		}
		if s := strings.TrimSpace(*l.in); s != "" {
			*l.out = s
		}
		if len(*l.out) > 100 {
			return nil, errInvalid("labels must be at most 100 characters")
		}
	}

	seen := map[uuid.UUID]bool{}
	var problems []string
	load := func(ids []uuid.UUID) []*report.Report {
		var out []*report.Report
		for _, id := range ids {
			if seen[id] {
				problems = append(problems, fmt.Sprintf("run %s is listed more than once", id))
				continue
			}
			seen[id] = true
			rep, problem, err := h.compareReport(ctx, p.OrgID, id)
			if err != nil {
				problems = append(problems, err.Error())
				continue
			}
			if problem != "" {
				problems = append(problems, problem)
				continue
			}
			out = append(out, rep)
		}
		return out
	}
	ra, rb := load(b.A), load(b.B)
	if len(problems) > 0 {
		return nil, errInvalid("every run must be in your organisation and have finished with a report", problems...)
	}

	c := report.Compare(ra, rb, labelA, labelB)
	var md bytes.Buffer
	c.WriteMarkdown(&md)
	out := gen.CompareRuns200JSONResponse{
		A:          gen.CompareSide{Label: c.A.Label, Runs: orEmpty(c.A.Runs)},
		B:          gen.CompareSide{Label: c.B.Label, Runs: orEmpty(c.B.Runs)},
		Comparable: c.Comparable,
		Problems:   orEmpty(c.Problems),
		Metrics:    metricDeltas(c.Metrics),
		Steps:      []gen.StepDelta{},
		Verdict:    gen.CompareVerdict(c.Verdict),
		Confidence: c.Confidence,
		Markdown:   md.String(),
	}
	for _, s := range c.Steps {
		out.Steps = append(out.Steps, gen.StepDelta{Journey: s.Journey, Step: s.Step, Metrics: metricDeltas(s.Metrics)})
	}
	return out, nil
}

// compareReport loads a run's report. A missing or unfinished run is a
// problem to report to the caller rather than an error.
func (h *handlers) compareReport(ctx context.Context, org, id uuid.UUID) (*report.Report, string, error) {
	r, err := h.st.GetRun(ctx, db.GetRunParams{ID: id, OrgID: org})
	if store.IsNotFound(err) {
		return nil, fmt.Sprintf("run %s not found", id), nil
	} else if err != nil {
		return nil, "", err
	}
	short := id.String()[:8]
	if r.Status != statusCompleted && r.Status != statusAborted {
		if r.Status == statusFailed {
			return nil, fmt.Sprintf("run %s failed before producing a report", short), nil
		}
		return nil, fmt.Sprintf("run %s has not finished (%s)", short, r.Status), nil
	}
	raw, err := h.st.GetReport(ctx, r.ID)
	if store.IsNotFound(err) {
		return nil, fmt.Sprintf("run %s has no report", short), nil
	} else if err != nil {
		return nil, "", err
	}
	rep, err := report.ReadJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("run %s: reading the report: %w", short, err)
	}
	rep.RunID = r.ID.String()
	return rep, "", nil
}

func metricDeltas(ms []report.MetricDelta) []gen.MetricDelta {
	out := make([]gen.MetricDelta, 0, len(ms))
	finite := func(f float64) *float64 {
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return nil
		}
		return &f
	}
	for _, m := range ms {
		out = append(out, gen.MetricDelta{
			Name: m.Name, HigherIsBetter: m.HigherIsBetter, A: orEmpty(m.A), B: orEmpty(m.B),
			MeanA: m.MeanA, MeanB: m.MeanB, Change: finite(m.Change), CiLow: finite(m.CILow), CiHigh: finite(m.CIHigh),
			NoiseFloor: m.NoiseFloor, Verdict: gen.CompareVerdict(m.Verdict),
		})
	}
	return out
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
