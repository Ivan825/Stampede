# EdTech pack

Journeys, stresses and targets for learning platforms and online exams:
courses, quizzes and timed exams with autosaved answers, a live channel
for the exam timer and proctoring heartbeats, and assignment
submissions.

| File | What it tests |
|---|---|
| `journeys/study-mix.yaml` | An ordinary day across 2,000 students: browse courses 40%, a five-question practice quiz with every answer saved and a score at the end 35%, hand in an assignment 15%, check a deadline 10% |
| `journeys/take-exam.yaml` | One student sits a timed exam: start the attempt, keep the exam's WebSocket open (timer down, heartbeats up, each heartbeat acknowledged), answer eight questions with autosave, submit |
| `stresses/exam-start.yaml` | Everyone starts at 10:00: after a quiet minute, arrivals jump to 300/s, each starting the same exam, opening its live channel and answering the first questions; starting under 1s, answers saved under 300ms |
| `stresses/deadline-rush.yaml` | Submissions climb from 5/s to 80/s before a deadline, each a 3 KB essay checked for similarity; submissions under 1s and confirmed |
| `targets.yaml` | Default targets: p95 under 500ms, under 1% errors, starting an exam under 1s, saving an answer under 300ms |

The journeys follow ExamLab's API: `POST /api/login` with `studentId`
and `password` for a bearer token, `/api/courses`, `/api/exams/{id}`,
`POST /api/exams/{id}/attempts` to start, `/api/attempts/{id}/questions/{n}`
and `PUT /api/attempts/{id}/answers/{n}` with `choice`, `POST
/api/attempts/{id}/submit`, the live channel `/api/exams/{id}/live?attempt=`
(sends `timer`, answers a `heartbeat` with `ack`, relays staff
`announcement`s), and `POST /api/assignments/{id}/submissions`. ExamLab
lets a student start an exam any number of times, so a load test can
reuse students. Every journey and stress is run against
[ExamLab](../../examples/packlab/README.md#examlab) in CI.

For your own platform, change the paths, the exam and assignment ids
under `vars`, and the answer payload; put test students in
`data/students.csv`. If your exam allows one attempt per student, give
`exam-start.yaml` at least as many students as it starts attempts, with
`mode: unique`. If your timer and proctoring use polling or server-sent
events instead of a socket, replace the `ws` block.

```sh
go run ./examples/packlab -product edtech            # ExamLab on :8101
stampede init --target http://localhost:8101         # detects this pack
stampede run stampede/edtech/stresses/exam-start.yaml -e TARGET_URL=http://localhost:8101
```
