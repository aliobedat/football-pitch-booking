# Follow-up: stale-response race in the financial reports fetches

**Origin:** WO-REPORTS-R3 runtime QA (2026-07-06). Deliberately NOT fixed in
that PR (guardrail: zero behavior changes to the R2 financial tab).

## Symptom

Fast period/pitch switching over slow responses lets a superseded request
overwrite the current one: the fetch effects in
`admin-dashboard/app/(dashboard)/reports/page.tsx` resolve with plain
`.then(setState)` and no cancellation, so last-response-wins regardless of
which selection it belongs to. Observed live during R3 QA: switching from the
current month to حزيران while the slower initial fetch was in flight rendered
July's rows and cards under a حزيران header. The same mechanism can render
stale financial summary/chart data or a mismatched month-over-month
comparison.

## Proven fix pattern

The effect-cleanup stale flag already implemented in
`admin-dashboard/components/BookingsReportTab.tsx` (verified stable in R3 QA):

```ts
useEffect(() => {
  let stale = false;
  api.get(...)
    .then(res => { if (!stale) setData(...); })
    .catch(err => { if (stale) return; ... })
    .finally(() => { if (!stale) setLoading(false); });
  return () => { stale = true; };
}, [deps]);
```

## Affected fetch sites in reports/page.tsx

1. The current-period `GET /owner/reports/financial` call (sets `data`/`error`
   and `loading`).
2. The parallel prior-month `GET /owner/reports/financial` call in the same
   effect (sets `prior` — a stale prior silently mislabels the comparison
   strip).
3. The once-on-mount `GET /owner/pitches` call (sets `pitches`) — low risk
   (fires once), but it can still resolve after unmount; guard it for
   consistency while touching the file.

Small mechanical PR; no visual or contract changes.
