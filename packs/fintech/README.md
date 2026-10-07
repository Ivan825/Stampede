# Fintech pack

Journeys, stresses and targets for banking and payments APIs: balances,
transaction history, statements, transfers and bill payments sent with an
Idempotency-Key, and a reconciliation check.

| File | What it tests |
|---|---|
| `journeys/banking-mix.yaml` | Everyday mix across 1,000 customers: balances and two pages of transactions 40%, a transfer to savings 20%, money sent to someone else 15%, a bill payment retried with the same key (the retry must return the first payment) 15%, a monthly statement 10% |
| `journeys/payment-errors.yaml` | No Idempotency-Key (400), a key reused for another amount (422), insufficient funds (422), someone else's account (403), no sign-in (401); then the ledger must still balance |
| `stresses/duplicate-payments.yaml` | 50 clients send the same 100 bill payments at once, each with its own key; a 409 "still processing" is retried after a second; each reference must have exactly one payment and the ledger must balance with no key used twice |
| `stresses/month-end-peak.yaml` | Arrivals rise from 5/s to 80/s: balance checks, bill payments and statement downloads together; payments under 300ms, balances under 200ms, statements under 1s |
| `targets.yaml` | Default targets: p95 under 500ms, under 1% errors, transfers under 300ms, statements under 1s |

The journeys follow BankLab's API: `POST /api/login` with `customerId`
and `password` for a bearer token, `GET /api/accounts`,
`/api/accounts/{id}/transactions` (newest first, `before` cursor),
`/api/accounts/{id}/statements` and `/statements/{YYYY-MM}`, `POST
/api/transfers` and `POST /api/payments` with an `Idempotency-Key` header
(a replay answers with the first result and `Idempotent-Replayed: true`;
a request while the first is still running gets 409 with `Retry-After`),
`GET /api/transfers?reference=` and `GET /api/ledger/check`. Amounts are
in cents. Every journey and stress is run against
[BankLab](../../examples/packlab/README.md#banklab) in CI.

The two correctness checks matter more than the timings. "One payment per
key" asks the API for every payment with the attempt's reference;
"ledger balances" is BankLab's reconciliation: every balance adds up to
zero across the bank and no key moved money twice. For your own API,
replace them with the queries your reconciliation uses, put test
customers in `data/customers.csv` and test payments in
`data/intents.csv`, and run against a test ledger. The pack never calls a
payment provider: Stampede's safety policy keeps requests on the target's
host, and AI-generated scenarios are kept away from payment providers'
hosts ([AI generation](../../docs/ai.md)).

```sh
go run ./examples/packlab -product fintech           # BankLab on :8097
stampede init --target http://localhost:8097         # detects this pack
stampede run stampede/fintech/stresses/duplicate-payments.yaml -e TARGET_URL=http://localhost:8097
```
