# WO-ADMIN-JOD-PRECISION

**Gate:** Gate 1 (implementation) — approved by Ali after the Gate 0 live Playwright audit.
**Branch:** `feat/admin-jod-precision` (from clean `main`).
**Scope owner:** admin-dashboard frontend, presentation layer only.

## Objective

Fix the one confirmed JOD presentation inconsistency found in the Gate 0 audit:
the Overview dashboard and the Analytics/financial dashboard render JOD amounts
with 2 decimals, while Reports and BookingSheet already render the correct
3-decimal fils precision.

**All JOD currency amounts on Overview and Analytics must render with exactly 3
decimal places.**

```
175      → 175.000
615.7    → 615.700
106.30   → 106.300
0        → 0.000
-2.5     → -2.500
```

## Approach

The canonical strict-3-decimal JOD formatter already exists (duplicated in four
report files, tracked for consolidation in `docs/followups/jod3-consolidation.md`):

```ts
const jod3 = (v: number) => formatCurrency(v, { minimumFractionDigits: 3, maximumFractionDigits: 3 });
```

Rather than add a 5th ad-hoc copy, this WO hoists that exact expression into the
shared `admin-dashboard/lib/format.ts` as an exported `jod3(value)` helper and
reuses it on the two in-scope surfaces. This is additive: `formatCurrency` keeps
its existing default behavior, and the four report copies are **not** touched
(their consolidation stays a separate follow-up, so no report output changes).

## Display sites changed (JOD only)

- `admin-dashboard/app/(dashboard)/page.tsx` — the single currency `KpiTile`
  render path (covers متوقّع اليوم، محصّل اليوم، متوقّع الأسبوع، محصّل الأسبوع،
  فجوة التحصيل). Count tiles keep `formatNumber`.
- `admin-dashboard/app/(dashboard)/analytics/page.tsx` — the line-chart tooltip
  value (labeled `د.أ`). Chart Y-axis ticks are bare (not JOD-labeled) and keep
  `formatNumber` per scope rule 5.
- `admin-dashboard/components/FinancialsSection.tsx` — net figure, the two
  equation legs (المحصّل / المصروفات), per-category subtotals, expense-ledger
  amounts, and the delete-confirm amount string.

## Explicitly NOT changed

Analytics calculations/formulas, API requests/response types, backend, DB,
Reports, printable report routes, BookingSheet, `amount_paid` NULL-as-untracked
behavior, revenue/collected/uncollected/expenses/net logic, auth/CSRF/cookies/
roles/middleware, notifications/Infobip, production config/data. Values are never
rounded or mutated — formatting is presentation-only. Out-of-scope audit findings
(Arabic 404, favicon, print behavior, token-refresh observation, stray text node)
remain separate follow-ups.

## Verification

TypeScript type-check, lint, production Next.js build for `admin-dashboard`, plus
Playwright checks at 1440×900 and 390×844 confirming 3-decimal rendering on the
Overview KPIs and the Analytics revenue/expenses/net figures, with no overflow,
no broken RTL, and no console/API errors introduced.
