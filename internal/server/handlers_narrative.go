package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/ai"
	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// CreateRunNarrative writes an AI summary into a finished run's report.
// It needs the editor role, like generation jobs, because it spends the
// organisation's AI tokens.
func (h *handlers) CreateRunNarrative(ctx context.Context, req gen.CreateRunNarrativeRequestObject) (gen.CreateRunNarrativeResponseObject, error) {
	r, err := h.run(ctx, req.RunId, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	// The role was checked in the run's project above.
	p, err := need(ctx, auth.PermView)
	if err != nil {
		return nil, err
	}
	raw, err := h.st.GetReport(ctx, r.ID)
	if err != nil {
		return nil, notFoundOr(err, "report (the run has not finished)")
	}
	rep, err := report.ReadJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var providerID *uuid.UUID
	if req.Body != nil {
		providerID = req.Body.ProviderId
	}
	prow, err := h.pickProvider(ctx, p.OrgID, providerID)
	if err != nil {
		return nil, err
	}
	used, err := h.aiTokensThisMonth(ctx, p.OrgID)
	if err != nil {
		return nil, err
	}
	if used >= prow.MonthlyTokenCap {
		return nil, &apiError{status: 429, code: "ai_token_cap", msg: fmt.Sprintf("the organisation used %d AI tokens this month, which reaches the cap of %d for provider %s", used, prow.MonthlyTokenCap, prow.Name)}
	}
	client, err := h.providerClient(prow)
	if err != nil {
		return nil, err
	}

	nctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	n, usage, nerr := ai.Narrate(nctx, rep, ai.NarrateOptions{Provider: client})
	// Tokens spent count even when the reply was unusable.
	if usage.Total() > 0 {
		uid, provID, runID := p.UserID, prow.ID, r.ID
		if err := h.st.RecordAIUsage(ctx, db.RecordAIUsageParams{
			ID: uuid.New(), OrgID: p.OrgID, ProviderID: &provID, Purpose: "narrative", RunID: &runID,
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CreatedBy: &uid,
		}); err != nil {
			return nil, err
		}
	}
	if nerr != nil {
		if errors.Is(nerr, context.Canceled) && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &apiError{status: 502, code: "ai_provider_error", msg: "the AI provider did not produce a usable summary: " + nerr.Error()}
	}
	rep.Narrative = n
	var buf bytes.Buffer
	if err := rep.WriteJSON(&buf); err != nil {
		return nil, err
	}
	if err := h.st.SaveReport(ctx, db.SaveReportParams{RunID: r.ID, Report: buf.Bytes()}); err != nil {
		return nil, err
	}
	h.audit(ctx, "run.narrative", r.ID.String(), map[string]any{"provider": prow.Name, "model": client.Model(),
		"claims": len(n.Claims), "inputTokens": usage.InputTokens, "outputTokens": usage.OutputTokens})

	out := gen.Narrative{Summary: n.Summary, Claims: []gen.NarrativeClaim{}, Facts: &[]gen.NarrativeFact{}}
	if n.Model != "" {
		m := n.Model
		out.Model = &m
	}
	for _, c := range n.Claims {
		out.Claims = append(out.Claims, gen.NarrativeClaim{Text: c.Text, Label: gen.NarrativeClaimLabel(c.Label), Refs: c.Refs})
	}
	for _, f := range n.Facts {
		*out.Facts = append(*out.Facts, gen.NarrativeFact{Id: f.ID, Text: f.Text, Where: f.Where})
	}
	return gen.CreateRunNarrative200JSONResponse{Narrative: out, Usage: gen.AIUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens}}, nil
}
