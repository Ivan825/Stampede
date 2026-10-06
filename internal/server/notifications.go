package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/keyring"
	"github.com/Ivan825/Stampede/internal/notify"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// NotifyConfig configures run notifications. Channels themselves are
// configured per organisation through the API.
type NotifyConfig struct {
	// Sender delivers events; tests replace its backoff. The default
	// retries after 2s, 10s and 30s.
	Sender *notify.Sender
	// PublicURL is the server's external base URL, used for links to runs
	// in notifications. Empty leaves the links out.
	PublicURL string
	// KeepDeliveries is how many attempts are kept per channel (default 50).
	KeepDeliveries int
	// Concurrency bounds deliveries in flight (default 8).
	Concurrency int
}

// notifier sends run events to an organisation's channels in the
// background and records every attempt in the delivery log.
type notifier struct {
	s      *Server
	sender *notify.Sender
	sem    chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newNotifier(s *Server) *notifier {
	c := &s.cfg.Notify
	if c.Sender == nil {
		c.Sender = &notify.Sender{}
	}
	if c.KeepDeliveries <= 0 {
		c.KeepDeliveries = 50
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 8
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	ctx, cancel := context.WithCancel(context.Background())
	return &notifier{s: s, sender: c.Sender, sem: make(chan struct{}, c.Concurrency), ctx: ctx, cancel: cancel}
}

// shutdown lets deliveries in flight finish until ctx ends, then cancels
// their retries.
func (n *notifier) shutdown(ctx context.Context) {
	done := make(chan struct{})
	go func() { n.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		n.cancel()
		<-done
	}
}

func channelAAD(org, id uuid.UUID, part string) []byte {
	return []byte("stampede-notification:" + org.String() + ":" + id.String() + ":" + part)
}

// open decrypts a channel's destination and secret.
func (n *notifier) open(row db.NotificationChannel) (notify.Channel, error) {
	ch := notify.Channel{ID: row.ID.String(), Name: row.Name, Kind: row.Kind, AllowPrivate: row.AllowPrivate}
	kr := n.s.cfg.Keyring
	if kr == nil {
		return ch, errors.New("the server has no master key, so channel URLs cannot be decrypted")
	}
	u, err := kr.Open(keyring.Sealed{Ciphertext: row.UrlCiphertext, WrappedKey: row.UrlWrappedKey, KeyID: row.KeyID}, channelAAD(row.OrgID, row.ID, "url"))
	if err != nil {
		return ch, fmt.Errorf("decrypt channel URL: %w", err)
	}
	ch.URL = string(u)
	if len(row.SecretCiphertext) > 0 {
		sec, err := kr.Open(keyring.Sealed{Ciphertext: row.SecretCiphertext, WrappedKey: row.SecretWrappedKey, KeyID: row.KeyID}, channelAAD(row.OrgID, row.ID, "secret"))
		if err != nil {
			return ch, fmt.Errorf("decrypt channel secret: %w", err)
		}
		ch.Secret = string(sec)
	}
	return ch, nil
}

// deliver sends ev to one channel, recording every attempt. With retry
// false only one attempt is made.
func (n *notifier) deliver(ctx context.Context, row db.NotificationChannel, ev notify.Event, runID *uuid.UUID, retry bool) (*db.NotificationDelivery, error) {
	bg := context.WithoutCancel(ctx)
	deliveryID, err := uuid.Parse(ev.ID)
	if err != nil {
		deliveryID = uuid.New()
		ev.ID = deliveryID.String()
	}
	var last *db.NotificationDelivery
	record := func(a notify.Attempt) {
		p := db.InsertNotificationDeliveryParams{
			ChannelID: row.ID, DeliveryID: deliveryID, Event: ev.Type, RunID: runID,
			Attempt: int32(a.N), Ok: a.OK, StatusCode: int32(a.Status), Error: a.Err, //nolint:gosec // small
			DurationMs: int32(a.Duration / time.Millisecond), //nolint:gosec // bounded by the client timeout
		}
		if err := n.s.st.InsertNotificationDelivery(bg, p); err != nil {
			n.s.log.Error("record notification delivery", "channel", row.Name, "error", err)
		}
		last = &db.NotificationDelivery{
			ChannelID: row.ID, DeliveryID: deliveryID, Event: ev.Type, RunID: runID, Attempt: p.Attempt,
			Ok: a.OK, StatusCode: p.StatusCode, Error: a.Err, DurationMs: p.DurationMs, At: a.At,
		}
	}
	ch, err := n.open(row)
	if err != nil {
		record(notify.Attempt{N: 1, Err: err.Error(), At: time.Now()})
	} else {
		sender := n.sender
		if !retry {
			s := *n.sender
			s.Backoff = []time.Duration{}
			sender = &s
		}
		err = sender.Send(ctx, ch, ev, record)
	}
	if perr := n.s.st.PruneNotificationDeliveries(bg, db.PruneNotificationDeliveriesParams{ChannelID: row.ID, Keep: int32(n.s.cfg.Notify.KeepDeliveries)}); perr != nil { //nolint:gosec // small
		n.s.log.Error("prune notification deliveries", "channel", row.Name, "error", perr)
	}
	return last, err
}

// publish sends ev to every channel of org subscribed to its type, in the
// background.
func (n *notifier) publish(org uuid.UUID, ev notify.Event, runID *uuid.UUID) {
	rows, err := n.s.st.ListNotificationChannelsForEvent(context.Background(), db.ListNotificationChannelsForEventParams{OrgID: org, Event: ev.Type})
	if err != nil {
		n.s.log.Error("list notification channels", "event", ev.Type, "error", err)
		return
	}
	for _, row := range rows {
		e := ev
		e.ID = uuid.NewString()
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			select {
			case n.sem <- struct{}{}:
			case <-n.ctx.Done():
				return
			}
			defer func() { <-n.sem }()
			if _, err := n.deliver(n.ctx, row, e, runID, true); err != nil {
				n.s.log.Warn("notification not delivered", "channel", row.Name, "event", e.Type, "error", err)
			}
		}()
	}
}

// runFinished publishes the events for a run that has ended: run.killed
// for a kill, otherwise run.finished, and run.target_failed as well when a
// target failed.
func (n *notifier) runFinished(r *activeRun, status, errMsg string, rep *report.Report) {
	ctx := context.Background()
	row, err := n.s.st.GetRun(ctx, db.GetRunParams{ID: r.id, OrgID: r.org})
	if err != nil {
		n.s.log.Error("notification: load run", "run", r.id, "error", err)
		return
	}
	info := &notify.Run{ID: r.id.String(), Scenario: row.ScenarioName, Target: row.TargetUrl, Status: status}
	if pr, err := n.s.st.GetProject(ctx, db.GetProjectParams{ID: r.project, OrgID: r.org}); err == nil {
		info.Project = pr.Name
	}
	if row.StopReason != nil {
		info.StopReason = *row.StopReason
	}
	if n.s.cfg.Notify.PublicURL != "" {
		info.URL = n.s.cfg.Notify.PublicURL + "/runs/" + r.id.String()
	}
	if rep != nil {
		info.Verdict = rep.Verdict
		o := rep.Overall
		info.Summary = &notify.Summary{Requests: o.Requests, ErrorRate: o.ErrorRate, RPS: o.RPS, P95: o.Latency.P95, P99: o.Latency.P99}
		for _, c := range rep.Thresholds {
			if !c.Pass {
				info.FailedTargets = append(info.FailedTargets, c.Source)
			}
		}
	}
	org := ""
	if o, err := n.s.st.GetOrg(ctx, r.org); err == nil {
		org = o.Name
	}
	now := time.Now().UTC()
	base := notify.Event{At: now, Org: org, Run: info}
	r.mu.Lock()
	killed, killedBy := r.killed, r.killedBy
	r.mu.Unlock()
	id := r.id
	switch {
	case killed:
		ev := base
		ev.Type = notify.EventRunKilled
		info.KilledBy = killedBy
		ev.Message = fmt.Sprintf("%s was killed with the kill switch", row.ScenarioName)
		n.publish(r.org, ev, &id)
		return
	case status == statusFailed:
		ev := base
		ev.Type = notify.EventRunFinished
		ev.Message = fmt.Sprintf("%s failed to run: %s", row.ScenarioName, errMsg)
		n.publish(r.org, ev, &id)
		return
	}
	ev := base
	ev.Type = notify.EventRunFinished
	ev.Message = fmt.Sprintf("%s finished: %s", row.ScenarioName, report.VerdictLabel(info.Verdict))
	n.publish(r.org, ev, &id)
	if info.Verdict == report.VerdictFail {
		tf := base
		tf.Type = notify.EventRunTargetFailed
		tf.Message = fmt.Sprintf("%s missed %d of %d targets", row.ScenarioName, len(info.FailedTargets), len(rep.Thresholds))
		n.publish(r.org, tf, &id)
	}
}

// Handlers.

func channelOf(r db.NotificationChannel, last *db.NotificationDelivery) gen.NotificationChannel {
	out := gen.NotificationChannel{
		Id: r.ID, Name: r.Name, Kind: gen.NotificationKind(r.Kind), AllowPrivate: r.AllowPrivate,
		UrlHint: r.UrlHint, HasSecret: len(r.SecretCiphertext) > 0, CreatedAt: r.CreatedAt,
		Events: []gen.NotificationEvent{},
	}
	for _, e := range r.Events {
		out.Events = append(out.Events, gen.NotificationEvent(e))
	}
	if last != nil {
		d := deliveryOf(*last)
		out.LastDelivery = &d
	}
	return out
}

func deliveryOf(d db.NotificationDelivery) gen.NotificationDelivery {
	return gen.NotificationDelivery{
		Id: d.ID, DeliveryId: d.DeliveryID, Event: d.Event, RunId: d.RunID, Attempt: int(d.Attempt), Ok: d.Ok,
		StatusCode: int(d.StatusCode), Error: d.Error, DurationMs: int(d.DurationMs), At: d.At,
	}
}

func (h *handlers) channel(ctx context.Context, id uuid.UUID) (*auth.Principal, db.NotificationChannel, error) {
	p, err := need(ctx, auth.PermManageUsers)
	if err != nil {
		return nil, db.NotificationChannel{}, err
	}
	row, err := h.st.GetNotificationChannel(ctx, db.GetNotificationChannelParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return nil, row, notFoundOr(err, "notification channel")
	}
	return p, row, nil
}

func (h *handlers) ListNotificationChannels(ctx context.Context, _ gen.ListNotificationChannelsRequestObject) (gen.ListNotificationChannelsResponseObject, error) {
	p, err := need(ctx, auth.PermManageUsers)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListNotificationChannels(ctx, p.OrgID)
	if err != nil {
		return nil, err
	}
	out := gen.ListNotificationChannels200JSONResponse{}
	for _, r := range rows {
		var last *db.NotificationDelivery
		if ds, err := h.st.ListNotificationDeliveries(ctx, db.ListNotificationDeliveriesParams{ChannelID: r.ID, Limit: 1}); err == nil && len(ds) > 0 {
			last = &ds[0]
		}
		out = append(out, channelOf(r, last))
	}
	return out, nil
}

func randomSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "whsec_" + base64.RawURLEncoding.EncodeToString(b)
}

func (h *handlers) CreateNotificationChannel(ctx context.Context, req gen.CreateNotificationChannelRequestObject) (gen.CreateNotificationChannelResponseObject, error) {
	p, err := need(ctx, auth.PermManageUsers)
	if err != nil {
		return nil, err
	}
	b := req.Body
	name := strings.TrimSpace(b.Name)
	if !scenario.IntegrationNameRe.MatchString(name) {
		return nil, errInvalid("name must start with a letter or digit and use letters, digits, '_', '.' or '-' (at most 100)")
	}
	kind := string(b.Kind)
	if !slices.Contains(notify.Kinds, kind) {
		return nil, errInvalid("kind must be webhook, slack or discord")
	}
	events := slices.Clone(notify.Events)
	if b.Events != nil {
		events = events[:0]
		for _, e := range *b.Events {
			if !slices.Contains(notify.Events, string(e)) {
				return nil, errInvalid(fmt.Sprintf("unknown event %q; use %s", e, strings.Join(notify.Events, ", ")))
			}
			if !slices.Contains(events, string(e)) {
				events = append(events, string(e))
			}
		}
		if len(events) == 0 {
			return nil, errInvalid("subscribe to at least one event")
		}
	}
	allowPrivate := b.AllowPrivate != nil && *b.AllowPrivate
	u, err := notify.CheckURL(ctx, b.Url, allowPrivate)
	if errors.Is(err, notify.ErrPrivateDestination) {
		return nil, errInvalid("the URL points to a private, loopback or link-local address; set allowPrivate to deliver there")
	}
	if err != nil {
		return nil, errInvalid("url: " + err.Error())
	}
	secret := ""
	if kind == notify.KindWebhook {
		secret = randomSecret()
		if b.Secret != nil && *b.Secret != "" {
			if len(*b.Secret) < 16 {
				return nil, errInvalid("secret must be at least 16 characters")
			}
			secret = *b.Secret
		}
	}
	kr, err := h.keyring()
	if err != nil {
		return nil, errConflict("notification channels are stored encrypted: start the server with STAMPEDE_MASTER_KEY set")
	}
	id := uuid.New()
	su, err := kr.Seal([]byte(u.String()), channelAAD(p.OrgID, id, "url"))
	if err != nil {
		return nil, err
	}
	var ss keyring.Sealed
	if secret != "" {
		if ss, err = kr.Seal([]byte(secret), channelAAD(p.OrgID, id, "secret")); err != nil {
			return nil, err
		}
	}
	hint := (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
	uid := p.UserID
	err = h.st.CreateNotificationChannel(ctx, db.CreateNotificationChannelParams{
		ID: id, OrgID: p.OrgID, Name: name, Kind: kind, Events: events, AllowPrivate: allowPrivate, UrlHint: hint,
		UrlCiphertext: su.Ciphertext, UrlWrappedKey: su.WrappedKey, SecretCiphertext: ss.Ciphertext, SecretWrappedKey: ss.WrappedKey,
		KeyID: su.KeyID, CreatedBy: &uid,
	})
	if err != nil {
		if store.IsUniqueViolation(err) {
			return nil, errConflict(fmt.Sprintf("a channel named %q already exists", name))
		}
		return nil, err
	}
	h.audit(ctx, "notification.channel.create", name, map[string]any{"kind": kind, "destination": hint, "events": events, "allowPrivate": allowPrivate})
	row, err := h.st.GetNotificationChannel(ctx, db.GetNotificationChannelParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return nil, err
	}
	out := gen.CreateNotificationChannel201JSONResponse{Channel: channelOf(row, nil)}
	if secret != "" {
		out.Secret = &secret
	}
	return out, nil
}

func (h *handlers) DeleteNotificationChannel(ctx context.Context, req gen.DeleteNotificationChannelRequestObject) (gen.DeleteNotificationChannelResponseObject, error) {
	p, row, err := h.channel(ctx, req.ChannelId)
	if err != nil {
		return nil, err
	}
	if _, err := h.st.DeleteNotificationChannel(ctx, db.DeleteNotificationChannelParams{ID: row.ID, OrgID: p.OrgID}); err != nil {
		return nil, err
	}
	h.audit(ctx, "notification.channel.delete", row.Name, nil)
	return gen.DeleteNotificationChannel204Response{}, nil
}

func (h *handlers) TestNotificationChannel(ctx context.Context, req gen.TestNotificationChannelRequestObject) (gen.TestNotificationChannelResponseObject, error) {
	p, row, err := h.channel(ctx, req.ChannelId)
	if err != nil {
		return nil, err
	}
	ev := notify.Event{
		ID: uuid.NewString(), Type: notify.EventTest, At: time.Now().UTC(), Org: p.OrgName,
		Message: "A test from Stampede, sent by " + p.Email + ". Run events will arrive like this.",
	}
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	last, _ := h.notify.deliver(dctx, row, ev, nil, false)
	h.audit(ctx, "notification.channel.test", row.Name, map[string]any{"ok": last != nil && last.Ok})
	if last == nil {
		return nil, errConflict("the test could not be sent")
	}
	ds, err := h.st.ListNotificationDeliveries(ctx, db.ListNotificationDeliveriesParams{ChannelID: row.ID, Limit: 1})
	if err == nil && len(ds) > 0 && ds[0].DeliveryID == last.DeliveryID {
		last = &ds[0]
	}
	return gen.TestNotificationChannel200JSONResponse(deliveryOf(*last)), nil
}

func (h *handlers) ListNotificationDeliveries(ctx context.Context, req gen.ListNotificationDeliveriesRequestObject) (gen.ListNotificationDeliveriesResponseObject, error) {
	_, row, err := h.channel(ctx, req.ChannelId)
	if err != nil {
		return nil, err
	}
	limit := int32(50)
	if req.Params.Limit != nil {
		limit = int32(min(max(*req.Params.Limit, 1), 50)) //nolint:gosec // clamped
	}
	ds, err := h.st.ListNotificationDeliveries(ctx, db.ListNotificationDeliveriesParams{ChannelID: row.ID, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := gen.ListNotificationDeliveries200JSONResponse{}
	for _, d := range ds {
		out = append(out, deliveryOf(d))
	}
	return out, nil
}
