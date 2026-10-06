# Government pack

Journeys, stresses and targets for government and public-sector portals:
exam results, notices and online applications with a deadline.

| File | What it tests |
|---|---|
| `journeys/citizen-mix.yaml` | An ordinary day: notices 30%; check a result by roll number and date of birth and download the PDF marksheet 55%; mistype the date of birth (a quick 404) and try again 15% |
| `journeys/apply.yaml` | A whole application: one-time-code sign-in, start, four sections saved in turn, a certificate upload, submission with an acknowledgement number, the PDF receipt, tracking |
| `stresses/results-day.yaml` | Results go live: arrivals spike from 5/s to 400/s, nearly all looking up a result and downloading the marksheet; lookups under 500ms, marksheets under 1s |
| `stresses/deadline-day.yaml` | The last hours before a deadline: whole applications climb from 1/s to 40/s; saves under 300ms, submissions under 500ms |
| `targets.yaml` | Default targets: p95 under 1s, under 1% errors, lookups under 500ms, marksheets under 1s |

The journeys follow GovLab's API: `POST /api/results/lookup` with
`rollNumber` and `dateOfBirth`, `GET
/api/results/{roll}/marksheet?dob=` (a PDF), `/api/notices`, `POST
/api/otp/request` and `/api/otp/verify` for a bearer token,
`/api/applications` with `PUT .../sections/{personal|address|education|income}`,
`POST .../documents?type=`, `POST .../submit` and `GET .../receipt`, and
`GET /api/acknowledgements/{ack}`. A wrong date of birth gets the same
404 as an unknown roll number. Every journey and stress is run against
[GovLab](../../examples/packlab/README.md#govlab) in CI.

**One-time codes.** GovLab's test mode returns the code in the response
(`testCode`) so the journeys can sign in; a real portal sends an SMS.
Never point these journeys at a portal that sends real messages: use its
staging environment with test numbers or a fixed test code, and change
the `verify code` step to match.

For your own portal, put real test roll numbers and dates of birth in
`data/candidates.csv`, test mobile numbers in `data/applicants.csv`, and
change the paths and form fields.

```sh
go run ./examples/packlab -product government        # GovLab on :8102
stampede init --target http://localhost:8102         # detects this pack
stampede run stampede/government/stresses/results-day.yaml -e TARGET_URL=http://localhost:8102
```
