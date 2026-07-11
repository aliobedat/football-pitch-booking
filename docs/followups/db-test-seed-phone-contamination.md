# DB-suite seed contamination: duplicate phone across parallel fixtures

> **STATUS 2026-07-11 — FIXED by WO-TEST-HYGIENE:** all four items below are
> addressed. Phone/slug suffixes now come from `internal/testutil.UniqueSuffix()`
> (process-seeded atomic counter — no same-microsecond collisions within a test
> binary). The reminder fixture seeds `role` + real pitch columns + a venue
> (`venue_id` is NOT NULL since migration 034). `TestSourceReaders_ReminderSkipsBlock`
> asserts on its own recipient phone instead of the global claim count.
> STILL MANUAL: run the repository package with `-timeout 25m` (go test's 10m
> default silently truncates it); re-baseline the scratch DB from
> `database/schema.sql` after panic-killed runs to clear orphan rows.

**Observed:** 2026-07-11, WO-VENUES Gate 1b precondition run (full suite on
scratch `scratch_gate1b`). `TestSourceCheck_PlayerWritePathUnchanged` failed
seeding its owner user with `duplicate key value violates unique constraint
"idx_users_phone_unique" (23505)`. It passes cleanly in isolation.

**Cause (pattern, not verified per-test):** most DB-backed fixtures build
test-user phone numbers as `+962<2-digit prefix><time.Now().UnixNano() %
1_000_000>`. Two envs constructed within the same microsecond (package-level
test parallelism in a full-suite run) with the same prefix collide on
`idx_users_phone_unique`.

**Ruling (Gate 1b, 2026-07-11):** documented only, NOT fixed in the venues
slice. Candidate fix for a test-hygiene PR: derive uniqueness from a
process-wide atomic counter (or `t.Name()` hash) instead of wall-clock nanos,
shared via a small test helper.

**Impact:** flaky full-suite runs only; no production surface. Retry or run
the failed package in isolation to confirm.

**Recurrences (same run day):** struck `TestManualWrite_InHoursPersistsGuest`
and `TestRecurring_AllOrNothingRollbackOnWeek3` (both seed via
`booking_block_test.go:62`) in two subsequent full-package runs — roughly one
random victim per full run. A zero-fail full-suite criterion is not reliably
achievable until this is fixed; prioritize the test-hygiene PR.

## Related discovery: repository package exceeds go test's default 10m timeout

Full-suite runs (`go test ./...`) hit `panic: test timed out after 10m0s` in
`internal/repository` against Neon, silently truncating the package — later
tests (e.g. the whole `TestReminder_*` set) never execute. Run the package
with `-timeout 25m`. This also means earlier "full suite green" runs that
clocked ~600s in repository were incomplete.

## Related: panic-killed runs leave orphan rows → global-claim tests flake

A `go test` run killed by the 10m timeout panic never executes `t.Cleanup`,
leaving confirmed player bookings (reminder_sent=false, future start) in the
shared scratch. `ClaimDueReminders` is GLOBAL (scans all bookings in the
window), so `TestSourceReaders_ReminderSkipsBlock` later counts the strays
("claimed 2, want 1") until something consumes them. Verified 2026-07-11:
failed twice on residue, passed immediately after the strays were claimed.
Fix options for the hygiene PR: scope the claim assertion to the test's
recipient phone, or periodic scratch re-baseline from schema.sql.

## Related: reminder fixture stale vs baseline

`reminder_repository_test.go` (`newReminderTestEnv`) seeds users WITHOUT
`role` (NOT NULL → 23502) and pitches with a `location` column that does not
exist. Deterministically broken against the canonical baseline; previously
masked by the 10m truncation. Needs the same fixture repair treatment as
PR#41 gave the other suites.
