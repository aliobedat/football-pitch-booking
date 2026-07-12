# Unknown enum value on pitch create/update → 500, should be 422

**Found during WO-FORMAT-6V6 Phase 0 (2026-07-12):** no Go code validates
`format` (or `surface`) values — `CreatePitchRequest.Format` is only
`binding:"required"` and the Postgres enum is the sole validator. A request
carrying an unknown value (e.g. a stale client sending a format the enum
doesn't have yet) fails the INSERT with SQLSTATE 22P02 (invalid input value
for enum), which the handler surfaces as a generic 500.

**Desired:** map enum-input failures to a 422 with a field-level error
(`invalid_format` / `invalid_surface`), mirroring the maps_url/label
conventions — either by catching 22P02 in the create/update paths or by
validating against the enum's values up front.

**Severity:** low — unreachable from the shipped UIs (both dashboards render
fixed selects), only malformed API calls hit it.

---
Status: open — documentation only, explicitly NOT fixed in WO-FORMAT-6V6.
