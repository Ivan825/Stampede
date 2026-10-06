// Package banklab is BankLab, a retail bank's API: customers with current
// and savings accounts, transaction history, monthly statements, transfers
// and bill payments that take an Idempotency-Key, and a reconciliation
// check. It is the reference app for the fintech pack. Bill payments go to
// the bank's own biller accounts: nothing leaves the app, and no payment
// provider is involved.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - lock: every transfer and payment holds one ledger-wide lock, fraud
//     check included, so money moves one transfer at a time and balance
//     reads queue behind it (fix "lock" runs the fraud check first and
//     locks only the two accounts involved).
//   - balance: an account's balance is the sum of all its postings,
//     recomputed for every balance read and every transfer, so it slows
//     down as history grows (fix "balance" keeps a running balance).
//   - statements: a monthly statement scans the whole bank's journal for
//     the account's postings (fix "statements" reads the account's own
//     postings, found by date).
package banklab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

// Customers is how many customers BankLab has (c0001 ... c1000).
const Customers = 1000

// Password is every customer's password.
const Password = "banklab-pass"

type posting struct {
	id       int64
	account  *account
	amount   int64 // minor units; negative is a debit
	at       time.Time
	desc     string
	transfer string
}

type account struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Currency string `json:"currency"`

	owner    string
	idx      int
	mu       sync.Mutex
	balance  int64 // running balance, kept in step with postings
	postings []*posting
}

type transfer struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	From      string `json:"fromAccount"`
	To        string `json:"toAccount"`
	Payee     string `json:"payee,omitempty"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Reference string `json:"reference"`
	Status    string `json:"status"`
	Created   string `json:"createdAt"`
	customer  string
}

// idem is one Idempotency-Key's record.
type idem struct {
	hash     string
	inflight bool
	status   int
	body     any
	created  int // transfers this key created (must never exceed one)
}

type payee struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	account *account
}

type server struct {
	cfg   labkit.Config
	fraud time.Duration // the fraud check's cost

	accounts map[string]*account
	byOwner  map[string][]*account
	payees   []*payee
	capital  *account

	// global is the ledger-wide lock (the lock bottleneck); snap lets
	// the reconciliation check see a consistent ledger with the fix.
	global sync.Mutex
	snap   sync.RWMutex

	jmu     sync.Mutex
	journal []*posting // every posting, oldest first
	nextID  int64

	tmu       sync.Mutex
	transfers map[string]*transfer
	byRef     map[string][]*transfer // customer/reference

	imu   sync.Mutex
	idems map[string]*idem

	smu      sync.RWMutex
	sessions map[string]string // token to customer

	summed   labkit.Counter // postings added up to compute balances
	scanned  labkit.Counter // journal postings scanned for statements
	inflight labkit.Gauge   // transfers inside the ledger's critical section
}

// New returns BankLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

var (
	merchants = []string{"Grocer", "Coffee Bar", "Fuel Station", "Bookshop", "Pharmacy", "Cinema", "Bakery", "Hardware Store"}
	// seedMonths are the closed months every account has history for.
	seedMonths = []time.Time{
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
)

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{
		cfg: cfg, fraud: 5 * time.Millisecond,
		accounts: map[string]*account{}, byOwner: map[string][]*account{},
		transfers: map[string]*transfer{}, byRef: map[string][]*transfer{},
		idems: map[string]*idem{}, sessions: map[string]string{},
	}
	if cfg.Fast {
		s.fraud = 100 * time.Microsecond
	}
	s.seed()
	routes := []labkit.Route{
		{Method: "POST", Path: "/api/login", Tag: "session", Summary: "Sign in with customerId and password; returns a bearer token"},
		{Method: "GET", Path: "/api/accounts", Tag: "accounts", Summary: "The customer's accounts with balances"},
		{Method: "GET", Path: "/api/accounts/{id}", Tag: "accounts", Summary: "One account with its balance"},
		{Method: "GET", Path: "/api/accounts/{id}/transactions", Tag: "transactions", Summary: "Recent transactions, newest first (limit, before)"},
		{Method: "GET", Path: "/api/accounts/{id}/statements", Tag: "statements", Summary: "Months with a statement"},
		{Method: "GET", Path: "/api/accounts/{id}/statements/{month}", Tag: "statements", Summary: "A monthly statement (YYYY-MM)"},
		{Method: "POST", Path: "/api/transfers", Tag: "transfers", Summary: "Move money; requires an Idempotency-Key header"},
		{Method: "GET", Path: "/api/transfers", Tag: "transfers", Summary: "The customer's transfers with a given reference"},
		{Method: "GET", Path: "/api/transfers/{id}", Tag: "transfers", Summary: "One transfer"},
		{Method: "GET", Path: "/api/payees", Tag: "payees", Summary: "Billers the customer can pay"},
		{Method: "POST", Path: "/api/payments", Tag: "payments", Summary: "Pay a biller; requires an Idempotency-Key header"},
		{Method: "GET", Path: "/api/ledger/check", Tag: "ledger", Summary: "Reconciliation: the ledger balances and no key moved money twice"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "BankLab", "links": map[string]string{"openapi": "/openapi.json"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("BankLab", "A retail bank's API: accounts, statements, idempotent transfers and bill payments, with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("GET /api/accounts", s.auth(s.listAccounts))
	mux.HandleFunc("GET /api/accounts/{id}", s.auth(s.getAccount))
	mux.HandleFunc("GET /api/accounts/{id}/transactions", s.auth(s.transactions))
	mux.HandleFunc("GET /api/accounts/{id}/statements", s.auth(s.statementList))
	mux.HandleFunc("GET /api/accounts/{id}/statements/{month}", s.auth(s.statement))
	mux.HandleFunc("POST /api/transfers", s.auth(s.createTransfer))
	mux.HandleFunc("GET /api/transfers", s.auth(s.findTransfers))
	mux.HandleFunc("GET /api/transfers/{id}", s.auth(s.getTransfer))
	mux.HandleFunc("GET /api/payees", s.auth(s.listPayees))
	mux.HandleFunc("POST /api/payments", s.auth(s.createPayment))
	mux.HandleFunc("GET /api/ledger/check", s.ledgerCheck)
	return s, mux
}

func (s *server) open(id, typ, name, owner string) *account {
	a := &account{ID: id, Type: typ, Name: name, Currency: "EUR", owner: owner, idx: len(s.accounts)}
	s.accounts[id] = a
	if owner != "" {
		s.byOwner[owner] = append(s.byOwner[owner], a)
	}
	return a
}

// post records a double entry: amount leaves from and reaches to. The
// caller holds whatever locks the ledger needs.
func (s *server) post(from, to *account, amount int64, at time.Time, desc, transferID string) {
	s.jmu.Lock()
	s.nextID += 2
	d := &posting{id: s.nextID - 1, account: from, amount: -amount, at: at, desc: desc, transfer: transferID}
	c := &posting{id: s.nextID, account: to, amount: amount, at: at, desc: desc, transfer: transferID}
	s.journal = append(s.journal, d, c)
	s.jmu.Unlock()
	from.postings = append(from.postings, d)
	from.balance -= amount
	to.postings = append(to.postings, c)
	to.balance += amount
}

// seed opens every account and gives each three months of history:
// opening balances, salaries, rent, card spending and savings, all
// double-entry against the bank's own house accounts so the ledger always
// sums to zero.
func (s *server) seed() {
	rng := rand.New(rand.NewPCG(7, 11))
	s.capital = s.open("acc_bank_capital", "house", "Bank capital", "")
	employer := s.open("acc_employer", "house", "Employers", "")
	cards := s.open("acc_card_merchants", "house", "Card merchants", "")
	for _, p := range []struct{ id, name string }{
		{"rent", "City Rentals"}, {"electric", "Electricity Co"}, {"water", "Water Board"},
		{"phone", "Mobile Network"}, {"internet", "Broadband Ltd"}, {"insurance", "Home Insurance"},
	} {
		s.payees = append(s.payees, &payee{ID: p.id, Name: p.name, account: s.open("acc_biller_"+p.id, "biller", p.name, "")})
	}
	opened := seedMonths[0].Add(-24 * time.Hour)
	for n := 1; n <= Customers; n++ {
		c := fmt.Sprintf("c%04d", n)
		cur := s.open(fmt.Sprintf("acc_%04d_cur", n), "current", "Current account", c)
		sav := s.open(fmt.Sprintf("acc_%04d_sav", n), "savings", "Savings account", c)
		s.post(s.capital, cur, 250_000, opened, "Opening balance", "")
		s.post(s.capital, sav, 2_000_000, opened, "Opening balance", "")
		for _, m := range seedMonths {
			s.post(employer, cur, 300_000+int64(rng.IntN(100_000)), m.Add(24*time.Hour), "Salary", "")
			s.post(cur, s.payees[0].account, 110_000, m.Add(48*time.Hour), "Rent", "")
			for d := 3; d < 28; d += 1 + rng.IntN(3) {
				s.post(cur, cards, 500+int64(rng.IntN(9_000)), m.Add(time.Duration(d)*24*time.Hour+time.Duration(rng.IntN(86400))*time.Second), "Card: "+merchants[rng.IntN(len(merchants))], "")
			}
			s.post(cur, sav, 20_000, m.Add(27*24*time.Hour), "Monthly saving", "")
		}
	}
}

// --- sessions -------------------------------------------------------------

type custKey struct{}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CustomerID string `json:"customerId"`
		Password   string `json:"password"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if len(s.byOwner[in.CustomerID]) == 0 || in.Password != Password {
		labkit.Error(w, 401, "invalid_credentials", "unknown customer or wrong password")
		return
	}
	tok := labkit.Token("bk_")
	s.smu.Lock()
	s.sessions[tok] = in.CustomerID
	s.smu.Unlock()
	labkit.JSON(w, 200, map[string]any{"token": tok, "customerId": in.CustomerID, "expiresIn": 900})
}

func (s *server) auth(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.smu.RLock()
		c, ok := s.sessions[labkit.Bearer(r)]
		s.smu.RUnlock()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="banklab"`)
			labkit.Error(w, 401, "unauthorized", "sign in at POST /api/login and send Authorization: Bearer <token>")
			return
		}
		next(w, r, c)
	}
}

// own returns one of the customer's accounts, answering 404 itself.
func (s *server) own(w http.ResponseWriter, id, customer string) *account {
	a := s.accounts[id]
	if a == nil || a.owner != customer {
		labkit.Error(w, 404, "not_found", "no such account")
		return nil
	}
	return a
}

// --- balances ---------------------------------------------------------------

// lockRead takes what a balance read needs: the ledger lock without the
// lock fix, the account's own lock with it.
func (s *server) lockRead(a *account) func() {
	if !s.cfg.Fixes.On("lock") {
		s.global.Lock()
		return s.global.Unlock
	}
	a.mu.Lock()
	return a.mu.Unlock
}

// balanceOf returns an account's balance; the caller holds its lock.
func (s *server) balanceOf(a *account) int64 {
	if s.cfg.Fixes.On("balance") {
		return a.balance
	}
	// Bottleneck: add up the account's whole history.
	var b int64
	for _, p := range a.postings {
		b += p.amount
	}
	s.summed.Add(len(a.postings))
	return b
}

func (s *server) view(a *account) map[string]any {
	unlock := s.lockRead(a)
	b := s.balanceOf(a)
	unlock()
	return map[string]any{"id": a.ID, "type": a.Type, "name": a.Name, "currency": a.Currency, "balance": b, "available": b}
}

func (s *server) listAccounts(w http.ResponseWriter, _ *http.Request, c string) {
	var out []map[string]any
	for _, a := range s.byOwner[c] {
		out = append(out, s.view(a))
	}
	labkit.JSON(w, 200, map[string]any{"accounts": out})
}

func (s *server) getAccount(w http.ResponseWriter, r *http.Request, c string) {
	if a := s.own(w, r.PathValue("id"), c); a != nil {
		labkit.JSON(w, 200, s.view(a))
	}
}

func txView(p *posting) map[string]any {
	return map[string]any{"id": "tx_" + strconv.FormatInt(p.id, 10), "amount": p.amount, "description": p.desc,
		"bookedAt": p.at.UTC().Format(time.RFC3339), "transferId": p.transfer}
}

// transactions lists recent postings, newest first, 20 by default, with
// a cursor (before=<posting id>) for older ones.
func (s *server) transactions(w http.ResponseWriter, r *http.Request, c string) {
	a := s.own(w, r.PathValue("id"), c)
	if a == nil {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	before, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Query().Get("before"), "tx_"), 10, 64)
	unlock := s.lockRead(a)
	var page []*posting
	more := false
	for i := len(a.postings) - 1; i >= 0; i-- {
		p := a.postings[i]
		if before > 0 && p.id >= before {
			continue
		}
		if len(page) == limit {
			more = true
			break
		}
		page = append(page, p)
	}
	unlock()
	out := make([]map[string]any, 0, len(page))
	for _, p := range page {
		out = append(out, txView(p))
	}
	next := ""
	if more {
		next = "tx_" + strconv.FormatInt(page[len(page)-1].id, 10)
	}
	labkit.JSON(w, 200, map[string]any{"account": a.ID, "transactions": out, "next_cursor": next})
}

// --- statements -------------------------------------------------------------

func (s *server) statementList(w http.ResponseWriter, r *http.Request, c string) {
	a := s.own(w, r.PathValue("id"), c)
	if a == nil {
		return
	}
	var out []map[string]string
	for i := len(seedMonths) - 1; i >= 0; i-- {
		m := seedMonths[i].Format("2006-01")
		out = append(out, map[string]string{"month": m, "href": "/api/accounts/" + a.ID + "/statements/" + m})
	}
	labkit.JSON(w, 200, map[string]any{"account": a.ID, "statements": out})
}

func (s *server) statement(w http.ResponseWriter, r *http.Request, c string) {
	a := s.own(w, r.PathValue("id"), c)
	if a == nil {
		return
	}
	start, err := time.Parse("2006-01", r.PathValue("month"))
	if err != nil {
		labkit.Error(w, 400, "bad_month", "month is YYYY-MM")
		return
	}
	end := start.AddDate(0, 1, 0)
	var opening int64
	var lines []*posting
	add := func(p *posting) {
		switch {
		case p.at.Before(start):
			opening += p.amount
		case p.at.Before(end):
			lines = append(lines, p)
		}
	}
	if s.cfg.Fixes.On("statements") {
		// Only the account's own postings.
		unlock := s.lockRead(a)
		for _, p := range a.postings {
			add(p)
		}
		unlock()
	} else {
		// Bottleneck: scan the whole bank's journal for this account.
		s.jmu.Lock()
		journal := s.journal
		s.jmu.Unlock()
		for _, p := range journal {
			if p.account == a {
				add(p)
			}
		}
		s.scanned.Add(len(journal))
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].at.Before(lines[j].at) })
	closing := opening
	var in, out int64
	txs := make([]map[string]any, 0, len(lines))
	for _, p := range lines {
		closing += p.amount
		if p.amount > 0 {
			in += p.amount
		} else {
			out -= p.amount
		}
		txs = append(txs, txView(p))
	}
	labkit.JSON(w, 200, map[string]any{"account": a.ID, "month": start.Format("2006-01"), "currency": a.Currency,
		"openingBalance": opening, "closingBalance": closing, "moneyIn": in, "moneyOut": out, "transactions": txs})
}

// --- money movement ---------------------------------------------------------

type moveRequest struct {
	From      string `json:"fromAccount"`
	To        string `json:"toAccount"`
	Payee     string `json:"payee"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Reference string `json:"reference"`
}

func (s *server) createTransfer(w http.ResponseWriter, r *http.Request, c string) {
	s.move(w, r, c, "transfer")
}

func (s *server) createPayment(w http.ResponseWriter, r *http.Request, c string) {
	s.move(w, r, c, "payment")
}

// move handles transfers and bill payments: Idempotency-Key bookkeeping,
// validation, then the ledger.
func (s *server) move(w http.ResponseWriter, r *http.Request, c, kind string) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" || len(key) > 255 {
		labkit.Error(w, 400, "idempotency_key_required", "send an Idempotency-Key header (up to 255 characters)")
		return
	}
	var in moveRequest
	if !labkit.Decode(w, r, &in) {
		return
	}
	if in.Currency == "" {
		in.Currency = "EUR"
	}
	b, _ := json.Marshal(in)
	sum := sha256.Sum256(append([]byte(kind), b...))
	hash := hex.EncodeToString(sum[:])

	ik := c + "/" + key
	s.imu.Lock()
	rec := s.idems[ik]
	switch {
	case rec != nil && rec.hash != hash:
		s.imu.Unlock()
		labkit.Error(w, 422, "idempotency_key_reused", "this Idempotency-Key was used with a different request")
		return
	case rec != nil && rec.inflight:
		s.imu.Unlock()
		w.Header().Set("Retry-After", "1")
		labkit.Error(w, 409, "request_in_progress", "a request with this Idempotency-Key is still being processed")
		return
	case rec != nil:
		s.imu.Unlock()
		w.Header().Set("Idempotent-Replayed", "true")
		w.Header().Set("Idempotency-Key", key)
		labkit.JSON(w, rec.status, rec.body)
		return
	}
	rec = &idem{hash: hash, inflight: true}
	s.idems[ik] = rec
	s.imu.Unlock()

	status, body, created := s.execute(c, kind, in)

	s.imu.Lock()
	rec.inflight, rec.status, rec.body = false, status, body
	if created {
		rec.created++
	}
	s.imu.Unlock()
	w.Header().Set("Idempotency-Key", key)
	labkit.JSON(w, status, body)
}

func apiError(code, msg string) map[string]any {
	return map[string]any{"error": map[string]string{"code": code, "message": msg}}
}

// execute validates and books one transfer or payment. It returns the
// response and whether money moved.
func (s *server) execute(c, kind string, in moveRequest) (int, any, bool) {
	from := s.accounts[in.From]
	if from == nil || from.owner != c {
		return 403, apiError("forbidden", "fromAccount is not one of your accounts"), false
	}
	var to *account
	var py *payee
	if kind == "payment" {
		for _, p := range s.payees {
			if p.ID == in.Payee {
				py, to = p, p.account
			}
		}
		if to == nil {
			return 422, apiError("unknown_payee", "no such payee"), false
		}
	} else {
		to = s.accounts[in.To]
		if to == nil || to.Type == "house" || to.Type == "biller" {
			return 422, apiError("unknown_account", "no such account"), false
		}
		if to == from {
			return 422, apiError("same_account", "fromAccount and toAccount are the same"), false
		}
	}
	if in.Amount <= 0 || in.Amount > 1_000_000 {
		return 422, apiError("bad_amount", "amount is in cents, from 1 to 1000000"), false
	}
	if in.Currency != from.Currency {
		return 422, apiError("currency_mismatch", "the account is in "+from.Currency), false
	}

	unlock := s.lockMove(from, to)
	defer unlock()
	if s.balanceOf(from) < in.Amount {
		return 422, apiError("insufficient_funds", "not enough money in "+from.ID), false
	}
	t := &transfer{ID: labkit.Token("tr_")[:19], Kind: kind, From: from.ID, To: to.ID, Amount: in.Amount,
		Currency: in.Currency, Reference: in.Reference, Status: "completed", Created: time.Now().UTC().Format(time.RFC3339Nano), customer: c}
	desc := "Transfer"
	if py != nil {
		t.Payee, desc = py.ID, "Payment to "+py.Name
	}
	if in.Reference != "" {
		desc += ": " + in.Reference
	}
	s.post(from, to, in.Amount, time.Now(), desc, t.ID)
	s.tmu.Lock()
	s.transfers[t.ID] = t
	if in.Reference != "" {
		s.byRef[c+"/"+in.Reference] = append(s.byRef[c+"/"+in.Reference], t)
	}
	s.tmu.Unlock()
	return 201, t, true
}

// lockMove takes the locks a transfer needs and runs the fraud check.
// Without the lock fix both happen under the one ledger lock.
func (s *server) lockMove(from, to *account) func() {
	if !s.cfg.Fixes.On("lock") {
		s.global.Lock()
		done := s.inflight.Enter()
		time.Sleep(s.fraud) // bottleneck: the fraud check runs under the ledger lock
		return func() { done(); s.global.Unlock() }
	}
	time.Sleep(s.fraud)
	s.snap.RLock()
	a, b := from, to
	if b.idx < a.idx {
		a, b = b, a
	}
	a.mu.Lock()
	b.mu.Lock()
	done := s.inflight.Enter()
	return func() { done(); b.mu.Unlock(); a.mu.Unlock(); s.snap.RUnlock() }
}

func (s *server) getTransfer(w http.ResponseWriter, r *http.Request, c string) {
	s.tmu.Lock()
	t := s.transfers[r.PathValue("id")]
	s.tmu.Unlock()
	if t == nil || t.customer != c {
		labkit.Error(w, 404, "not_found", "no such transfer")
		return
	}
	labkit.JSON(w, 200, t)
}

func (s *server) findTransfers(w http.ResponseWriter, r *http.Request, c string) {
	ref := r.URL.Query().Get("reference")
	if ref == "" {
		labkit.Error(w, 400, "reference_required", "give ?reference=")
		return
	}
	s.tmu.Lock()
	out := append([]*transfer{}, s.byRef[c+"/"+ref]...)
	s.tmu.Unlock()
	labkit.JSON(w, 200, map[string]any{"reference": ref, "transfers": out})
}

func (s *server) listPayees(w http.ResponseWriter, _ *http.Request, _ string) {
	labkit.JSON(w, 200, map[string]any{"payees": s.payees})
}

// ledgerCheck is the reconciliation a bank runs: every account's balance
// adds up to zero across the bank (money is only ever moved, never made),
// and no Idempotency-Key moved money more than once.
func (s *server) ledgerCheck(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.Fixes.On("lock") {
		s.snap.Lock()
		defer s.snap.Unlock()
	} else {
		s.global.Lock()
		defer s.global.Unlock()
	}
	var total int64
	for _, a := range s.accounts {
		total += a.balance
	}
	s.imu.Lock()
	dups, keys := 0, len(s.idems)
	for _, rec := range s.idems {
		if rec.created > 1 {
			dups++
		}
	}
	s.imu.Unlock()
	s.tmu.Lock()
	n := len(s.transfers)
	s.tmu.Unlock()
	labkit.JSON(w, 200, map[string]any{"balanced": total == 0, "total": total, "transfers": n, "idempotencyKeys": keys, "duplicates": dups})
}
