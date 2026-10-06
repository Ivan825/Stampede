// Package examlab is ExamLab, an online learning and exam platform:
// courses, timed exams taken question by question with every answer
// saved as it is given, a WebSocket channel per exam for the timer,
// proctoring heartbeats and announcements, and assignment submissions
// checked for similarity. It is the reference app for the edtech pack.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - start: starting an attempt shuffles the whole question bank and
//     writes the attempt while holding the exam's one lock, so when
//     everyone starts at 10:00 they start one at a time (fix "start"
//     hands out pre-built paper variants and writes outside the lock).
//   - autosave: saved answers go into one log for every attempt, and an
//     attempt's answers are found by scanning that log (200ns a row, like
//     a table scan), so each save costs more as the exam goes on (fix
//     "autosave" keeps each attempt's answers with the attempt).
//   - similarity: every submission is compared with every earlier
//     submission to the assignment, so the deadline rush gets slower with
//     each one (fix "similarity" looks fingerprints up in an index).
package examlab

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"maps"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

const (
	// Students is how many students ExamLab has (s00001 ... s05000).
	Students = 5000
	// Password is every student's password.
	Password = "examlab-pass"
	// StaffToken is the bearer token staff post announcements with.
	StaffToken = "examlab-staff"

	bankSize  = 2000
	perPaper  = 40
	variants  = 32
	kgram     = 8
	winnowWin = 4
	commonFP  = 50
)

type question struct {
	ID      int      `json:"id"`
	Text    string   `json:"text"`
	Choices []string `json:"choices"`
	answer  int
}

type exam struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Course   string `json:"course"`
	Minutes  int    `json:"durationMinutes"`
	Question int    `json:"questions"`
	OpensAt  string `json:"opensAt"`

	mu    sync.Mutex // the exam's one lock (start bottleneck)
	subs  map[*sub]bool
	smu   sync.Mutex
	paper [][]int // pre-built variants (start fix)
}

type attempt struct {
	ID        string
	exam      *exam
	student   string
	questions []int // bank indexes
	started   time.Time
	ends      time.Time

	mu        sync.Mutex
	answers   map[int]int // question number to choice (autosave fix)
	submitted bool
	score     int
}

type save struct {
	attempt string
	n       int
	choice  int
}

type assignment struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Course   string `json:"course"`
	Due      string `json:"dueAt"`
	due      time.Time
	mu       sync.Mutex
	subs     [][]uint64          // fingerprints of every submission, in order
	index    map[uint64][]int32  // fingerprint to submissions (similarity fix)
	received map[string][]string // student to submission ids
}

type sub struct {
	out chan []byte
}

type server struct {
	cfg     labkit.Config
	dbWrite time.Duration
	rowCost time.Duration // reading one row in a scan
	tick    time.Duration
	bank    []question
	exams   map[string]*exam
	assigns map[string]*assignment
	courses []map[string]any

	smu      sync.RWMutex
	sessions map[string]string // token to student

	amu      sync.RWMutex
	attempts map[string]*attempt

	lmu  sync.Mutex
	log  []save // every saved answer of every attempt (autosave bottleneck)
	next int

	startGauge labkit.Gauge   // attempts being written at once
	scanned    labkit.Counter // log entries scanned to find an attempt's answers
	compared   labkit.Counter // submission pairs compared
}

// New returns ExamLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

var topics = []string{"Algebra", "Biology", "Chemistry", "History", "Literature", "Physics", "Statistics", "Economics", "Geography", "Computing"}

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{cfg: cfg, dbWrite: 5 * time.Millisecond, rowCost: 200 * time.Nanosecond, tick: 30 * time.Second, exams: map[string]*exam{}, assigns: map[string]*assignment{},
		sessions: map[string]string{}, attempts: map[string]*attempt{}}
	if cfg.Fast {
		s.dbWrite, s.rowCost, s.tick = 50*time.Microsecond, 2*time.Nanosecond, 300*time.Millisecond
	}
	rng := rand.New(rand.NewPCG(10, 0))
	for i := range bankSize {
		q := question{ID: i + 1, Text: fmt.Sprintf("%s question %d: which statement is correct?", topics[i%len(topics)], i+1), answer: rng.IntN(4)}
		for c := range 4 {
			q.Choices = append(q.Choices, fmt.Sprintf("Statement %c", 'A'+c))
		}
		s.bank = append(s.bank, q)
	}
	now := time.Now()
	for i, t := range topics {
		cid := fmt.Sprintf("C%d", 101+i)
		s.courses = append(s.courses, map[string]any{"id": cid, "title": "Introduction to " + t, "students": Students / 2})
		e := &exam{ID: fmt.Sprintf("EX-%d", 101+i), Title: "Midterm: " + t, Course: cid, Minutes: 60, Question: perPaper,
			OpensAt: now.Add(-time.Minute).UTC().Format(time.RFC3339), subs: map[*sub]bool{}}
		for range variants {
			e.paper = append(e.paper, rng.Perm(bankSize)[:perPaper])
		}
		s.exams[e.ID] = e
		a := &assignment{ID: fmt.Sprintf("A-%d", 101+i), Title: t + " essay", Course: cid, due: now.Add(time.Hour),
			index: map[uint64][]int32{}, received: map[string][]string{}}
		a.Due = a.due.UTC().Format(time.RFC3339)
		s.assigns[a.ID] = a
	}
	routes := []labkit.Route{
		{Method: "POST", Path: "/api/login", Tag: "session", Summary: "Sign in with studentId and password; returns a bearer token"},
		{Method: "GET", Path: "/api/courses", Tag: "courses", Summary: "Courses"},
		{Method: "GET", Path: "/api/courses/{id}", Tag: "courses", Summary: "A course with its exams and assignments"},
		{Method: "GET", Path: "/api/exams", Tag: "exams", Summary: "Exams open now"},
		{Method: "GET", Path: "/api/exams/{id}", Tag: "exams", Summary: "One exam"},
		{Method: "POST", Path: "/api/exams/{id}/attempts", Tag: "attempts", Summary: "Start an attempt"},
		{Method: "GET", Path: "/api/exams/{id}/live", Tag: "proctoring", Summary: "WebSocket (?attempt=): timer, heartbeats, announcements"},
		{Method: "POST", Path: "/api/exams/{id}/announcements", Tag: "proctoring", Summary: "Announce to everyone taking the exam (staff token)"},
		{Method: "GET", Path: "/api/attempts/{id}", Tag: "attempts", Summary: "An attempt: answered, remaining time, score once submitted"},
		{Method: "GET", Path: "/api/attempts/{id}/questions/{n}", Tag: "attempts", Summary: "Question n (1-40) of the attempt"},
		{Method: "PUT", Path: "/api/attempts/{id}/answers/{n}", Tag: "attempts", Summary: "Save the answer to question n"},
		{Method: "POST", Path: "/api/attempts/{id}/submit", Tag: "attempts", Summary: "Submit the attempt for marking"},
		{Method: "GET", Path: "/api/assignments/{id}", Tag: "assignments", Summary: "An assignment and its deadline"},
		{Method: "POST", Path: "/api/assignments/{id}/submissions", Tag: "submissions", Summary: "Submit work (checked for similarity)"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "ExamLab", "links": map[string]string{"openapi": "/openapi.json"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("ExamLab", "Courses, timed exams with autosave and a live channel, and assignment submissions, with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("GET /api/courses", s.auth(s.listCourses))
	mux.HandleFunc("GET /api/courses/{id}", s.auth(s.getCourse))
	mux.HandleFunc("GET /api/exams", s.auth(s.listExams))
	mux.HandleFunc("GET /api/exams/{id}", s.auth(s.getExam))
	mux.HandleFunc("POST /api/exams/{id}/attempts", s.auth(s.startAttempt))
	mux.HandleFunc("GET /api/exams/{id}/live", s.auth(s.live))
	mux.HandleFunc("POST /api/exams/{id}/announcements", s.announce)
	mux.HandleFunc("GET /api/attempts/{id}", s.auth(s.getAttempt))
	mux.HandleFunc("GET /api/attempts/{id}/questions/{n}", s.auth(s.getQuestion))
	mux.HandleFunc("PUT /api/attempts/{id}/answers/{n}", s.auth(s.saveAnswer))
	mux.HandleFunc("POST /api/attempts/{id}/submit", s.auth(s.submit))
	mux.HandleFunc("GET /api/assignments/{id}", s.auth(s.getAssignment))
	mux.HandleFunc("POST /api/assignments/{id}/submissions", s.auth(s.submitWork))
	return s, mux
}

// --- sessions -------------------------------------------------------------

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		StudentID string `json:"studentId"`
		Password  string `json:"password"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	n, err := strconv.Atoi(strings.TrimPrefix(in.StudentID, "s"))
	if !strings.HasPrefix(in.StudentID, "s") || err != nil || n < 1 || n > Students || in.Password != Password {
		labkit.Error(w, 401, "invalid_credentials", "unknown student or wrong password")
		return
	}
	tok := labkit.Token("ex_")
	s.smu.Lock()
	s.sessions[tok] = in.StudentID
	s.smu.Unlock()
	labkit.JSON(w, 200, map[string]any{"token": tok, "studentId": in.StudentID})
}

func (s *server) auth(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.smu.RLock()
		st, ok := s.sessions[labkit.Bearer(r)]
		s.smu.RUnlock()
		if !ok {
			labkit.Error(w, 401, "unauthorized", "sign in at POST /api/login and send Authorization: Bearer <token>")
			return
		}
		next(w, r, st)
	}
}

// --- courses and exams ------------------------------------------------------

func (s *server) listCourses(w http.ResponseWriter, _ *http.Request, _ string) {
	labkit.JSON(w, 200, map[string]any{"courses": s.courses})
}

func (s *server) getCourse(w http.ResponseWriter, r *http.Request, _ string) {
	id := r.PathValue("id")
	for i, c := range s.courses {
		if c["id"] == id {
			e, a := s.exams[fmt.Sprintf("EX-%d", 101+i)], s.assigns[fmt.Sprintf("A-%d", 101+i)]
			labkit.JSON(w, 200, map[string]any{"course": c, "exams": []*exam{e}, "assignments": []*assignment{a},
				"materials": []string{"Week 1 slides", "Week 2 slides", "Reading list", "Past paper"}})
			return
		}
	}
	labkit.Error(w, 404, "not_found", "no such course")
}

func (s *server) listExams(w http.ResponseWriter, _ *http.Request, _ string) {
	out := make([]*exam, 0, len(s.exams))
	for i := range topics {
		out = append(out, s.exams[fmt.Sprintf("EX-%d", 101+i)])
	}
	labkit.JSON(w, 200, map[string]any{"exams": out})
}

func (s *server) getExam(w http.ResponseWriter, r *http.Request, _ string) {
	if e := s.exams[r.PathValue("id")]; e != nil {
		labkit.JSON(w, 200, e)
		return
	}
	labkit.Error(w, 404, "not_found", "no such exam")
}

func (s *server) startAttempt(w http.ResponseWriter, r *http.Request, st string) {
	e := s.exams[r.PathValue("id")]
	if e == nil {
		labkit.Error(w, 404, "not_found", "no such exam")
		return
	}
	now := time.Now()
	a := &attempt{exam: e, student: st, started: now, ends: now.Add(time.Duration(e.Minutes) * time.Minute), answers: map[int]int{}}
	if s.cfg.Fixes.On("start") {
		h := fnv.New32a()
		h.Write([]byte(st))
		a.questions = e.paper[h.Sum32()%variants]
		done := s.startGauge.Enter()
		time.Sleep(s.dbWrite) // write the attempt; no exam-wide lock
		done()
	} else {
		// Bottleneck: build the paper and write the attempt under the
		// exam's one lock.
		e.mu.Lock()
		done := s.startGauge.Enter()
		a.questions = rand.Perm(bankSize)[:perPaper]
		time.Sleep(s.dbWrite)
		done()
		e.mu.Unlock()
	}
	s.amu.Lock()
	s.next++
	a.ID = fmt.Sprintf("at_%06d", s.next)
	s.attempts[a.ID] = a
	s.amu.Unlock()
	labkit.JSON(w, 201, map[string]any{"attemptId": a.ID, "examId": e.ID, "questions": perPaper,
		"startedAt": a.started.UTC().Format(time.RFC3339), "endsAt": a.ends.UTC().Format(time.RFC3339)})
}

// attempt finds one of the student's attempts, answering 404 itself.
func (s *server) attempt(w http.ResponseWriter, r *http.Request, st string) *attempt {
	s.amu.RLock()
	a := s.attempts[r.PathValue("id")]
	s.amu.RUnlock()
	if a == nil || a.student != st {
		labkit.Error(w, 404, "not_found", "no such attempt")
		return nil
	}
	return a
}

func questionNumber(w http.ResponseWriter, r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 || n > perPaper {
		labkit.Error(w, 404, "not_found", "questions are numbered 1 to 40")
		return 0, false
	}
	return n, true
}

func (s *server) getQuestion(w http.ResponseWriter, r *http.Request, st string) {
	a := s.attempt(w, r, st)
	if a == nil {
		return
	}
	n, ok := questionNumber(w, r)
	if !ok {
		return
	}
	q := s.bank[a.questions[n-1]]
	labkit.JSON(w, 200, map[string]any{"n": n, "of": perPaper, "id": q.ID, "text": q.Text, "choices": q.Choices})
}

// answers returns the attempt's current answers.
func (s *server) answers(a *attempt) map[int]int {
	if s.cfg.Fixes.On("autosave") {
		a.mu.Lock()
		defer a.mu.Unlock()
		return maps.Clone(a.answers)
	}
	// Bottleneck: scan every saved answer of every attempt.
	s.lmu.Lock()
	log := s.log
	s.lmu.Unlock()
	out := map[int]int{}
	for _, sv := range log {
		if sv.attempt == a.ID {
			out[sv.n] = sv.choice
		}
	}
	s.scanned.Add(len(log))
	time.Sleep(time.Duration(len(log)) * s.rowCost) // a table scan reads every row
	return out
}

func (s *server) saveAnswer(w http.ResponseWriter, r *http.Request, st string) {
	a := s.attempt(w, r, st)
	if a == nil {
		return
	}
	n, ok := questionNumber(w, r)
	if !ok {
		return
	}
	var in struct {
		Choice *int `json:"choice"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if in.Choice == nil || *in.Choice < 0 || *in.Choice > 3 {
		labkit.Error(w, 422, "bad_choice", "choice is 0 to 3")
		return
	}
	a.mu.Lock()
	closed := a.submitted || time.Now().After(a.ends)
	if !closed {
		a.answers[n] = *in.Choice
	}
	a.mu.Unlock()
	if closed {
		labkit.Error(w, 409, "attempt_closed", "the attempt is submitted or out of time")
		return
	}
	time.Sleep(s.dbWrite / 5) // the answer row
	s.lmu.Lock()
	s.log = append(s.log, save{attempt: a.ID, n: n, choice: *in.Choice})
	s.lmu.Unlock()
	answered := len(s.answers(a))
	labkit.JSON(w, 200, map[string]any{"saved": true, "question": n, "answered": answered, "remainingSeconds": int(time.Until(a.ends).Seconds())})
}

func (s *server) getAttempt(w http.ResponseWriter, r *http.Request, st string) {
	a := s.attempt(w, r, st)
	if a == nil {
		return
	}
	ans := s.answers(a)
	a.mu.Lock()
	out := map[string]any{"attemptId": a.ID, "examId": a.exam.ID, "answered": len(ans), "questions": perPaper,
		"remainingSeconds": max(0, int(time.Until(a.ends).Seconds())), "submitted": a.submitted}
	if a.submitted {
		out["score"] = a.score
	}
	a.mu.Unlock()
	labkit.JSON(w, 200, out)
}

func (s *server) submit(w http.ResponseWriter, r *http.Request, st string) {
	a := s.attempt(w, r, st)
	if a == nil {
		return
	}
	ans := s.answers(a)
	a.mu.Lock()
	if a.submitted {
		a.mu.Unlock()
		labkit.Error(w, 409, "already_submitted", "this attempt was submitted already")
		return
	}
	score := 0
	for n, c := range ans {
		if s.bank[a.questions[n-1]].answer == c {
			score++
		}
	}
	a.submitted, a.score = true, score
	a.mu.Unlock()
	time.Sleep(s.dbWrite)
	labkit.JSON(w, 200, map[string]any{"attemptId": a.ID, "submitted": true, "answered": len(ans), "score": score, "outOf": perPaper,
		"submittedAt": time.Now().UTC().Format(time.RFC3339)})
}

// --- the live channel -------------------------------------------------------

func (s *server) live(w http.ResponseWriter, r *http.Request, st string) {
	e := s.exams[r.PathValue("id")]
	if e == nil {
		labkit.Error(w, 404, "not_found", "no such exam")
		return
	}
	s.amu.RLock()
	a := s.attempts[r.URL.Query().Get("attempt")]
	s.amu.RUnlock()
	if a == nil || a.student != st || a.exam != e {
		labkit.Error(w, 404, "not_found", "give ?attempt= with one of your attempts at this exam")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(16 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sb := &sub{out: make(chan []byte, 64)}
	e.smu.Lock()
	e.subs[sb] = true
	e.smu.Unlock()
	defer func() {
		e.smu.Lock()
		delete(e.subs, sb)
		e.smu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				cancel()
				return
			}
			var in struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &in) == nil && in.Type == "heartbeat" {
				b, _ := json.Marshal(map[string]any{"type": "ack", "remaining": int(time.Until(a.ends).Seconds())})
				select {
				case sb.out <- b:
				default:
				}
			}
		}
	}()
	timer := func() []byte {
		b, _ := json.Marshal(map[string]any{"type": "timer", "attemptId": a.ID, "remaining": max(0, int(time.Until(a.ends).Seconds()))})
		return b
	}
	t := time.NewTicker(s.tick)
	defer t.Stop()
	write := func(b []byte) bool {
		wctx, c := context.WithTimeout(ctx, 5*time.Second)
		defer c()
		return conn.Write(wctx, websocket.MessageText, b) == nil
	}
	if !write(timer()) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-sb.out:
			if !write(b) {
				return
			}
		case <-t.C:
			if !write(timer()) {
				return
			}
		}
	}
}

func (s *server) announce(w http.ResponseWriter, r *http.Request) {
	if labkit.Bearer(r) != StaffToken {
		labkit.Error(w, 401, "unauthorized", "staff only: send Authorization: Bearer <staff token>")
		return
	}
	e := s.exams[r.PathValue("id")]
	if e == nil {
		labkit.Error(w, 404, "not_found", "no such exam")
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	b, _ := json.Marshal(map[string]any{"type": "announcement", "text": in.Text})
	e.smu.Lock()
	n := 0
	for sb := range e.subs {
		select {
		case sb.out <- b:
			n++
		default:
		}
	}
	e.smu.Unlock()
	labkit.JSON(w, 200, map[string]any{"delivered": n})
}

// --- assignments --------------------------------------------------------

func (s *server) getAssignment(w http.ResponseWriter, r *http.Request, st string) {
	a := s.assigns[r.PathValue("id")]
	if a == nil {
		labkit.Error(w, 404, "not_found", "no such assignment")
		return
	}
	a.mu.Lock()
	mine := len(a.received[st])
	a.mu.Unlock()
	labkit.JSON(w, 200, map[string]any{"assignment": a, "yourSubmissions": mine})
}

// fingerprints winnows a text's k-gram hashes, as plagiarism checkers do:
// the smallest hash of every window of four, deduplicated and sorted.
func fingerprints(text string) []uint64 {
	t := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if len(t) < kgram {
		return nil
	}
	hs := make([]uint64, 0, len(t)-kgram+1)
	for i := 0; i+kgram <= len(t); i++ {
		h := fnv.New64a()
		h.Write([]byte(t[i : i+kgram]))
		hs = append(hs, h.Sum64())
	}
	var fp []uint64
	for i := 0; i+winnowWin <= len(hs); i++ {
		fp = append(fp, slices.Min(hs[i:i+winnowWin]))
	}
	slices.Sort(fp)
	return slices.Compact(fp)
}

// overlap counts fingerprints two sorted lists share.
func overlap(a, b []uint64) int {
	n, i, j := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			n++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return n
}

func (s *server) submitWork(w http.ResponseWriter, r *http.Request, st string) {
	a := s.assigns[r.PathValue("id")]
	if a == nil {
		labkit.Error(w, 404, "not_found", "no such assignment")
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if len(strings.TrimSpace(in.Text)) < 100 {
		labkit.Error(w, 422, "too_short", "submissions are at least 100 characters")
		return
	}
	fp := fingerprints(in.Text)
	a.mu.Lock()
	best := 0
	if s.cfg.Fixes.On("similarity") {
		counts := map[int32]int{}
		for _, f := range fp {
			for _, k := range a.index[f] {
				counts[k]++
			}
		}
		for _, c := range counts {
			best = max(best, c)
		}
		for _, f := range fp {
			// Fingerprints most submissions share (a title, a quoted
			// question) say nothing about copying; stop indexing them.
			if len(a.index[f]) < commonFP {
				a.index[f] = append(a.index[f], int32(len(a.subs)))
			}
		}
	} else {
		// Bottleneck: compare with every earlier submission.
		for _, other := range a.subs {
			best = max(best, overlap(fp, other))
		}
		s.compared.Add(len(a.subs))
	}
	a.subs = append(a.subs, fp)
	id := fmt.Sprintf("sub_%s_%06d", a.ID, len(a.subs))
	a.received[st] = append(a.received[st], id)
	a.mu.Unlock()
	similarity := 0.0
	if len(fp) > 0 {
		similarity = float64(best) / float64(len(fp))
	}
	now := time.Now()
	labkit.JSON(w, 201, map[string]any{"submissionId": id, "assignmentId": a.ID, "receivedAt": now.UTC().Format(time.RFC3339Nano),
		"late": now.After(a.due), "similarity": float64(int(similarity*1000)) / 1000})
}
