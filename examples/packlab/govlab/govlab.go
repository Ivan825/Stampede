// Package govlab is GovLab, a government services portal: exam results
// looked up by roll number and date of birth with downloadable PDF
// marksheets, notices, and an online application (sign-in by one-time
// code, a form in sections, document upload, submission with an
// acknowledgement number and a PDF receipt) with a deadline. It is the
// reference app for the government pack.
//
// Every query goes through a database pool of ten connections, as a
// portal's does; a query holds a connection for as long as it runs.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - index: results are found by scanning all 100,000 rows (200ns a
//     row, 20ms a lookup) while holding a pool connection, so on results
//     day the pool runs out (fix "index" looks the roll number up).
//   - pdf: every marksheet and receipt compresses the 400 KB letterhead
//     image again (fix "pdf" compresses it once and reuses the bytes).
//   - lock: every application save takes one portal-wide lock and writes
//     the whole application and an audit entry (2ms) before letting go,
//     so on deadline day saves run one at a time (fix "lock" locks only
//     the application being saved).
package govlab

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

const (
	// Candidates is how many exam results GovLab holds; roll numbers run
	// from FirstRoll upward.
	Candidates = 100_000
	// FirstRoll is the first roll number.
	FirstRoll = 26_000_001
)

var subjects = []string{"English", "Mathematics", "Physics", "Chemistry", "Biology", "Computer Science"}

type result struct {
	roll  int
	dob   string
	name  string
	marks [6]int
}

// DOB is a candidate's date of birth (deterministic, so test data can be
// generated outside the app).
func DOB(roll int) string {
	return time.Date(2008, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, roll*7919%1000).Format("2006-01-02")
}

type application struct {
	ID       string            `json:"applicationId"`
	Scheme   string            `json:"scheme"`
	Status   string            `json:"status"`
	Sections map[string]any    `json:"sections"`
	Docs     map[string]string `json:"documents"`
	Ack      string            `json:"acknowledgementNumber,omitempty"`
	Updated  string            `json:"updatedAt"`
	mobile   string
	mu       sync.Mutex
}

var required = []string{"personal", "address", "education", "income"}

type server struct {
	cfg     labkit.Config
	rowCost time.Duration
	write   time.Duration
	pool    chan struct{}

	results []result
	byRoll  map[int]int // the index (index fix)

	letter     []byte // the letterhead image, uncompressed
	letterOnce sync.Once
	letterZ    []byte // compressed once (pdf fix)

	omu  sync.Mutex
	otps map[string]otp

	smu      sync.RWMutex
	sessions map[string]string // token to mobile

	global sync.Mutex // the portal-wide lock (lock bottleneck)
	amu    sync.RWMutex
	apps   map[string]*application
	acks   map[string]*application
	next   int
	audit  []string
	dead   time.Time

	scanned     labkit.Counter // result rows scanned
	compressed  labkit.Counter // letterhead compressions
	savesInLock labkit.Gauge   // application saves inside the critical section
}

type otp struct {
	code, mobile string
	exp          time.Time
}

// New returns GovLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{cfg: cfg, rowCost: 200 * time.Nanosecond, write: 2 * time.Millisecond, pool: make(chan struct{}, 10),
		byRoll: map[int]int{}, otps: map[string]otp{}, sessions: map[string]string{},
		apps: map[string]*application{}, acks: map[string]*application{}, dead: time.Now().Add(48 * time.Hour)}
	letterSize := 400 << 10
	if cfg.Fast {
		s.rowCost, s.write, letterSize = 0, 20*time.Microsecond, 8<<10
	}
	first := []string{"Aarav", "Diya", "Kabir", "Meera", "Rohan", "Sara", "Vihaan", "Anaya", "Arjun", "Isha"}
	last := []string{"Sharma", "Iyer", "Khan", "Das", "Patel", "Reddy", "Singh", "Gupta", "Nair", "Bose"}
	s.results = make([]result, Candidates)
	for i := range s.results {
		roll := FirstRoll + i
		r := result{roll: roll, dob: DOB(roll), name: first[roll%10] + " " + last[(roll/10)%10]}
		for k := range r.marks {
			r.marks[k] = 25 + (roll*(k+3)*2654435761>>7)%76
		}
		s.results[i] = r
		s.byRoll[roll] = i
	}
	// A letterhead image: smooth gradients compress, as scanned art does.
	s.letter = make([]byte, letterSize)
	for i := range s.letter {
		s.letter[i] = byte(i/97 + i%13)
	}
	routes := []labkit.Route{
		{Method: "GET", Path: "/api/notices", Tag: "notices", Summary: "Notices and announcements"},
		{Method: "GET", Path: "/api/results/status", Tag: "results", Summary: "Whether results are published"},
		{Method: "POST", Path: "/api/results/lookup", Tag: "results", Summary: "A candidate's result by rollNumber and dateOfBirth"},
		{Method: "GET", Path: "/api/results/{roll}/marksheet", Tag: "results", Summary: "Marksheet PDF (?dob=)"},
		{Method: "POST", Path: "/api/otp/request", Tag: "citizens", Summary: "Send a one-time code to a mobile number (test mode returns it)"},
		{Method: "POST", Path: "/api/otp/verify", Tag: "citizens", Summary: "Exchange the code for a bearer token"},
		{Method: "POST", Path: "/api/applications", Tag: "applications", Summary: "Start an application"},
		{Method: "GET", Path: "/api/applications/{id}", Tag: "applications", Summary: "An application"},
		{Method: "PUT", Path: "/api/applications/{id}/sections/{section}", Tag: "applications", Summary: "Save a section: personal, address, education, income"},
		{Method: "POST", Path: "/api/applications/{id}/documents", Tag: "applications", Summary: "Upload a document (?type=)"},
		{Method: "POST", Path: "/api/applications/{id}/submit", Tag: "applications", Summary: "Submit; returns the acknowledgement number"},
		{Method: "GET", Path: "/api/applications/{id}/receipt", Tag: "acknowledgements", Summary: "Acknowledgement receipt PDF"},
		{Method: "GET", Path: "/api/acknowledgements/{ack}", Tag: "acknowledgements", Summary: "Track an application by acknowledgement number"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "GovLab", "links": map[string]string{"openapi": "/openapi.json"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("GovLab", "A government portal: exam results and online applications with a deadline, with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/notices", s.notices)
	mux.HandleFunc("GET /api/results/status", s.resultStatus)
	mux.HandleFunc("POST /api/results/lookup", s.lookup)
	mux.HandleFunc("GET /api/results/{roll}/marksheet", s.marksheet)
	mux.HandleFunc("POST /api/otp/request", s.otpRequest)
	mux.HandleFunc("POST /api/otp/verify", s.otpVerify)
	mux.HandleFunc("POST /api/applications", s.auth(s.createApp))
	mux.HandleFunc("GET /api/applications/{id}", s.auth(s.getApp))
	mux.HandleFunc("PUT /api/applications/{id}/sections/{section}", s.auth(s.saveSection))
	mux.HandleFunc("POST /api/applications/{id}/documents", s.auth(s.upload))
	mux.HandleFunc("POST /api/applications/{id}/submit", s.auth(s.submit))
	mux.HandleFunc("GET /api/applications/{id}/receipt", s.auth(s.receipt))
	mux.HandleFunc("GET /api/acknowledgements/{ack}", s.track)
	return s, mux
}

// query runs on one of the pool's connections.
func (s *server) query(cost time.Duration, f func()) {
	s.pool <- struct{}{}
	defer func() { <-s.pool }()
	if cost > 0 {
		time.Sleep(cost)
	}
	f()
}

func (s *server) notices(w http.ResponseWriter, _ *http.Request) {
	var out []map[string]string
	s.query(200*time.Microsecond, func() {
		out = []map[string]string{
			{"id": "N-311", "title": "Class XII results 2026 are published", "date": time.Now().UTC().Format("2006-01-02")},
			{"id": "N-310", "title": "Scholarship 2026: applications close " + s.dead.UTC().Format("2 January 2006 15:04 MST"), "date": time.Now().AddDate(0, 0, -3).UTC().Format("2006-01-02")},
			{"id": "N-305", "title": "Revaluation requests open after results", "date": time.Now().AddDate(0, 0, -10).UTC().Format("2006-01-02")},
		}
	})
	labkit.JSON(w, 200, map[string]any{"notices": out})
}

func (s *server) resultStatus(w http.ResponseWriter, _ *http.Request) {
	labkit.JSON(w, 200, map[string]any{"exam": "Class XII 2026", "published": true, "candidates": Candidates})
}

// find returns a candidate's row, or -1.
func (s *server) find(roll int) int {
	idx := -1
	if s.cfg.Fixes.On("index") {
		s.query(50*time.Microsecond, func() {
			if i, ok := s.byRoll[roll]; ok {
				idx = i
			}
		})
		return idx
	}
	// Bottleneck: no index; scan the table on a pool connection.
	s.query(time.Duration(Candidates)*s.rowCost, func() {
		for i := range s.results {
			if s.results[i].roll == roll {
				idx = i
				break
			}
		}
		s.scanned.Add(Candidates)
	})
	return idx
}

func grade(m int) string {
	switch {
	case m >= 91:
		return "A1"
	case m >= 81:
		return "A2"
	case m >= 71:
		return "B1"
	case m >= 61:
		return "B2"
	case m >= 51:
		return "C1"
	case m >= 41:
		return "C2"
	case m >= 33:
		return "D"
	}
	return "E"
}

func (s *server) view(r *result) map[string]any {
	total, failed := 0, 0
	var subs []map[string]any
	for k, m := range r.marks {
		total += m
		if m < 33 {
			failed++
		}
		subs = append(subs, map[string]any{"subject": subjects[k], "marks": m, "grade": grade(m)})
	}
	status := "PASS"
	if failed > 0 {
		status = "COMPARTMENT"
	}
	return map[string]any{"rollNumber": strconv.Itoa(r.roll), "name": r.name, "exam": "Class XII 2026", "subjects": subs,
		"total": total, "maximum": 600, "percentage": float64(total*1000/600) / 10, "result": status}
}

// candidate finds a result by roll number and date of birth; both must
// match, and a mismatch looks the same as an unknown roll number.
func (s *server) candidate(w http.ResponseWriter, roll, dob string) *result {
	n, err := strconv.Atoi(roll)
	if err != nil {
		labkit.Error(w, 400, "bad_roll_number", "roll numbers are digits")
		return nil
	}
	i := s.find(n)
	if i < 0 || s.results[i].dob != dob {
		labkit.Error(w, 404, "not_found", "no result for this roll number and date of birth")
		return nil
	}
	return &s.results[i]
}

func (s *server) lookup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Roll string `json:"rollNumber"`
		DOB  string `json:"dateOfBirth"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if res := s.candidate(w, in.Roll, in.DOB); res != nil {
		labkit.JSON(w, 200, s.view(res))
	}
}

// letterhead returns the compressed letterhead image.
func (s *server) letterhead() []byte {
	if s.cfg.Fixes.On("pdf") {
		s.letterOnce.Do(func() { s.letterZ = s.compress() })
		return s.letterZ
	}
	return s.compress() // bottleneck: compress it again for every document
}

func (s *server) compress() []byte {
	s.compressed.Add(1)
	var b bytes.Buffer
	z, _ := zlib.NewWriterLevel(&b, zlib.BestCompression)
	_, _ = z.Write(s.letter)
	_ = z.Close()
	return b.Bytes()
}

// pdf builds a small, valid PDF: the letterhead image and lines of text.
func (s *server) pdf(title string, lines []string) []byte {
	img := s.letterhead()
	var text bytes.Buffer
	text.WriteString("BT /F1 12 Tf 50 760 Td 16 TL\n")
	for _, l := range append([]string{title, ""}, lines...) {
		fmt.Fprintf(&text, "(%s) '\n", strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(l))
	}
	text.WriteString("ET\n")
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> /XObject << /Im1 5 0 R >> >> /Contents 6 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream", len(s.letter), len(img), img),
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", text.Len(), text.String()),
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

func writePDF(w http.ResponseWriter, name string, b []byte) {
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}

func (s *server) marksheet(w http.ResponseWriter, r *http.Request) {
	res := s.candidate(w, r.PathValue("roll"), r.URL.Query().Get("dob"))
	if res == nil {
		return
	}
	v := s.view(res)
	lines := []string{"Roll number: " + v["rollNumber"].(string), "Name: " + res.name}
	for _, sub := range v["subjects"].([]map[string]any) {
		lines = append(lines, fmt.Sprintf("%s: %d (%s)", sub["subject"], sub["marks"], sub["grade"]))
	}
	lines = append(lines, fmt.Sprintf("Total: %d / 600  Result: %s", v["total"], v["result"]))
	writePDF(w, "marksheet-"+r.PathValue("roll")+".pdf", s.pdf("Statement of Marks, Class XII 2026", lines))
}

// --- sign-in by one-time code -------------------------------------------

func (s *server) otpRequest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mobile string `json:"mobile"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if len(in.Mobile) != 10 || strings.Trim(in.Mobile, "0123456789") != "" || in.Mobile[0] < '6' {
		labkit.Error(w, 422, "bad_mobile", "a 10-digit mobile number")
		return
	}
	id := labkit.Token("otp_")[:20]
	code := fmt.Sprintf("%06d", int(id[10])*7919%1_000_000)
	s.omu.Lock()
	s.otps[id] = otp{code: code, mobile: in.Mobile, exp: time.Now().Add(5 * time.Minute)}
	s.omu.Unlock()
	// Test mode: the code comes back in the response instead of an SMS.
	labkit.JSON(w, 200, map[string]any{"requestId": id, "expiresIn": 300, "testCode": code})
}

func (s *server) otpVerify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RequestID string `json:"requestId"`
		Code      string `json:"code"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	s.omu.Lock()
	o, ok := s.otps[in.RequestID]
	if ok {
		delete(s.otps, in.RequestID)
	}
	s.omu.Unlock()
	if !ok || o.code != in.Code || time.Now().After(o.exp) {
		labkit.Error(w, 401, "bad_code", "wrong or expired code")
		return
	}
	tok := labkit.Token("gv_")
	s.smu.Lock()
	s.sessions[tok] = o.mobile
	s.smu.Unlock()
	labkit.JSON(w, 200, map[string]any{"token": tok, "expiresIn": 1800})
}

func (s *server) auth(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.smu.RLock()
		m, ok := s.sessions[labkit.Bearer(r)]
		s.smu.RUnlock()
		if !ok {
			labkit.Error(w, 401, "unauthorized", "sign in with a one-time code and send Authorization: Bearer <token>")
			return
		}
		next(w, r, m)
	}
}

// --- applications -------------------------------------------------------

func (s *server) createApp(w http.ResponseWriter, _ *http.Request, mobile string) {
	if time.Now().After(s.dead) {
		labkit.Error(w, 409, "closed", "applications have closed")
		return
	}
	a := &application{Scheme: "Scholarship 2026", Status: "draft", Sections: map[string]any{}, Docs: map[string]string{}, mobile: mobile}
	s.amu.Lock()
	s.next++
	a.ID = fmt.Sprintf("APP-2026-%07d", s.next)
	s.apps[a.ID] = a
	s.amu.Unlock()
	s.saveApp(a, func() { a.Updated = time.Now().UTC().Format(time.RFC3339) })
	labkit.JSON(w, 201, s.snapshot(a))
}

func (s *server) app(w http.ResponseWriter, r *http.Request, mobile string) *application {
	s.amu.RLock()
	a := s.apps[r.PathValue("id")]
	s.amu.RUnlock()
	if a == nil || a.mobile != mobile {
		labkit.Error(w, 404, "not_found", "no such application")
		return nil
	}
	return a
}

func (s *server) snapshot(a *application) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	secs := map[string]any{}
	for k, v := range a.Sections {
		secs[k] = v
	}
	docs := map[string]string{}
	for k, v := range a.Docs {
		docs[k] = v
	}
	out := map[string]any{"applicationId": a.ID, "scheme": a.Scheme, "status": a.Status, "sections": secs, "documents": docs,
		"updatedAt": a.Updated, "deadline": s.dead.UTC().Format(time.RFC3339)}
	if a.Ack != "" {
		out["acknowledgementNumber"] = a.Ack
	}
	return out
}

// saveApp applies a change and writes the application with an audit
// entry. Without the lock fix it does so under the one portal-wide lock.
func (s *server) saveApp(a *application, change func()) {
	if !s.cfg.Fixes.On("lock") {
		s.global.Lock()
		defer s.global.Unlock()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	done := s.savesInLock.Enter()
	defer done()
	change()
	time.Sleep(s.write) // the application row and the audit entry, flushed
	s.amu.Lock()
	s.audit = append(s.audit, a.ID+" "+a.Updated)
	if len(s.audit) > 10000 {
		s.audit = append(s.audit[:0:0], s.audit[5000:]...)
	}
	s.amu.Unlock()
}

func (s *server) getApp(w http.ResponseWriter, r *http.Request, mobile string) {
	if a := s.app(w, r, mobile); a != nil {
		labkit.JSON(w, 200, s.snapshot(a))
	}
}

func (s *server) saveSection(w http.ResponseWriter, r *http.Request, mobile string) {
	a := s.app(w, r, mobile)
	if a == nil {
		return
	}
	sec := r.PathValue("section")
	ok := false
	for _, x := range required {
		ok = ok || x == sec
	}
	if !ok {
		labkit.Error(w, 404, "not_found", "sections are personal, address, education and income")
		return
	}
	var fields map[string]any
	if !labkit.Decode(w, r, &fields) {
		return
	}
	if len(fields) == 0 {
		labkit.Error(w, 422, "empty_section", "send the section's fields")
		return
	}
	var closed bool
	s.saveApp(a, func() {
		if closed = a.Status != "draft"; !closed {
			a.Sections[sec] = fields
			a.Updated = time.Now().UTC().Format(time.RFC3339)
		}
	})
	if closed {
		labkit.Error(w, 409, "submitted", "the application was submitted already")
		return
	}
	labkit.JSON(w, 200, map[string]any{"saved": sec, "applicationId": a.ID})
}

func (s *server) upload(w http.ResponseWriter, r *http.Request, mobile string) {
	a := s.app(w, r, mobile)
	if a == nil {
		return
	}
	typ := r.URL.Query().Get("type")
	if typ == "" {
		typ = "document"
	}
	n, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil || n < 100 {
		labkit.Error(w, 422, "bad_document", "documents are 100 bytes to 2 MB")
		return
	}
	id := labkit.Token("doc_")[:16]
	s.saveApp(a, func() {
		a.Docs[typ] = id
		a.Updated = time.Now().UTC().Format(time.RFC3339)
	})
	labkit.JSON(w, 201, map[string]any{"documentId": id, "type": typ, "bytes": n})
}

func (s *server) submit(w http.ResponseWriter, r *http.Request, mobile string) {
	a := s.app(w, r, mobile)
	if a == nil {
		return
	}
	var missing []string
	var already bool
	s.saveApp(a, func() {
		if already = a.Status != "draft"; already {
			return
		}
		for _, x := range required {
			if a.Sections[x] == nil {
				missing = append(missing, x)
			}
		}
		if len(missing) == 0 {
			a.Status, a.Ack = "submitted", "ACK"+strings.TrimPrefix(a.ID, "APP-")
			a.Updated = time.Now().UTC().Format(time.RFC3339)
		}
	})
	switch {
	case already:
		labkit.Error(w, 409, "submitted", "the application was submitted already")
	case len(missing) > 0:
		labkit.Error(w, 422, "incomplete", "missing sections: "+strings.Join(missing, ", "))
	default:
		s.amu.Lock()
		s.acks[a.Ack] = a
		s.amu.Unlock()
		labkit.JSON(w, 200, map[string]any{"applicationId": a.ID, "status": "submitted", "acknowledgementNumber": a.Ack, "submittedAt": a.Updated})
	}
}

func (s *server) receipt(w http.ResponseWriter, r *http.Request, mobile string) {
	a := s.app(w, r, mobile)
	if a == nil {
		return
	}
	v := s.snapshot(a)
	ack, _ := v["acknowledgementNumber"].(string)
	if ack == "" {
		labkit.Error(w, 409, "not_submitted", "submit the application first")
		return
	}
	writePDF(w, "receipt-"+ack+".pdf", s.pdf("Acknowledgement: Scholarship 2026", []string{"Acknowledgement number: " + ack, "Application: " + a.ID, "Submitted: " + v["updatedAt"].(string)}))
}

func (s *server) track(w http.ResponseWriter, r *http.Request) {
	s.amu.RLock()
	a := s.acks[r.PathValue("ack")]
	s.amu.RUnlock()
	if a == nil {
		labkit.Error(w, 404, "not_found", "no application with this acknowledgement number")
		return
	}
	v := s.snapshot(a)
	labkit.JSON(w, 200, map[string]any{"acknowledgementNumber": v["acknowledgementNumber"], "status": "received", "scheme": v["scheme"], "submittedAt": v["updatedAt"]})
}
