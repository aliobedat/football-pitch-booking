# Follow-up: consolidate the `jod3` strict-3dp JOD formatter

**Logged by:** WO-REPORTS-R4 (ruling B1), 2026-07-07

The one-line strict-3-decimal JOD wrapper

```ts
const jod3 = (v: number) => formatCurrency(v, { minimumFractionDigits: 3, maximumFractionDigits: 3 });
```

now exists in **four** copies:

1. `admin-dashboard/app/(dashboard)/reports/page.tsx` (R2)
2. `admin-dashboard/components/BookingsReportTab.tsx` (R3)
3. `admin-dashboard/app/(print)/reports/print/financial/page.tsx` (R4)
4. `admin-dashboard/app/(print)/reports/print/bookings/page.tsx` (R4)

Per ruling B1 the R4 print components duplicated it rather than touching the
shared `lib/format.ts` mid-initiative. Once the reports initiative is closed,
hoist `jod3` into `admin-dashboard/lib/format.ts` (exported alongside
`formatCurrency`) and delete the four local copies in one mechanical PR.

Acceptance for the consolidation PR: no rendering change anywhere the four
copies were used (identical output strings), `tsc` clean.
