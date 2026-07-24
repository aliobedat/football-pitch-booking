# Follow-up: stranded demo rebrand + untested dbadmin -create-owner (WIP, not landed)

**Recorded:** 2026-07-25, during WO-CROSS-MIDNIGHT closeout. Both items sit as
UNCOMMITTED edits in the working tree; they are outside WO-CROSS-MIDNIGHT scope
and were deliberately NOT committed. They belong to a separate future WO.

## 1. Stranded sama-amman demo rebrand (`backend/cmd/demo-reset/main.go`)

The pushed demo-reset tool (commit `3fedee5`) seeds the OLD placeholder
branding: owner «أحمد الدمو», venue «ملعب الدمو الرياضي» / slug
`demo-sports-venue`, pitches «الملعب الرئيسي/الثانوي». An uncommitted edit
renames all of these to the «ملاعب سما عمان» branding (slug `sama-amman`,
pitches «ملعب سما عمان 1/2»). Until this lands, any demo-reset run produces the
old names — the rebrand exists only in one working tree.

**To land:** commit the rename (constants only, no logic change) and re-run the
demo-reset test suite (`cmd/demo-reset/main_test.go`) against a stamped scratch
baseline, since the tests may assert on the seeded names.

## 2. dbadmin `-create-owner` tool (`backend/cmd/dbadmin/main.go`, +64 lines)

An uncommitted `-create-owner` flag provisions a new owner user (phone +
full_name + bcrypt password), insert-only with a refuse-if-phone-exists guard
(never re-roles or overwrites an existing account). Design looks sound, but it
must NOT land until:

- (a) **Test coverage exists.** The flag has zero tests — a DB write path
  landing untested violates the standing "DB writes are proven, not assumed"
  principle. Minimum: creates-owner happy path, refuses on existing phone of
  ANY role, phone normalisation, password via env `DBADMIN_PASSWORD` vs flag.
- (b) **The real phone number is scrubbed.** The doc-comment usage example
  embeds a real number (`+962795303606`). Replace with an obvious placeholder
  (e.g. `+9627XXXXXXXX`) before any commit — real personal numbers must not
  enter source control.

## Status

Both edits remain in the working tree untouched, exactly as found. Do not
commit, revert, or "clean up" either file under an unrelated mandate — they are
owner-owned WIP pending their own WO.
