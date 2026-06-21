package models

import "time"

// Expense is one owner-scoped ledger entry (Cockpit WO-F2). Amount mirrors
// bookings.total_price (NUMERIC(10,3) JOD/fils). pitch_id NULL = business-wide
// overhead — never misattributed to a pitch. Soft-deleted rows are excluded from
// reads. Category is one of the fixed preset keys (DB CHECK).
type Expense struct {
	ID         int64     `json:"id"`
	PitchID    *int64    `json:"pitch_id"`
	PitchName  *string   `json:"pitch_name,omitempty"`
	Category   string    `json:"category"`
	Amount     float64   `json:"amount"`
	OccurredAt time.Time `json:"occurred_at"`
	Note       *string   `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ExpenseCategories is the canonical preset set (mirrors the expenses_category_chk
// constraint). The UI maps these keys to Arabic labels.
var ExpenseCategories = map[string]bool{
	"Electricity": true,
	"Staff":       true,
	"Water":       true,
	"Maintenance": true,
	"Marketing":   true,
	"Other":       true,
}

// IsValidExpenseCategory reports whether c is an accepted category key.
func IsValidExpenseCategory(c string) bool { return ExpenseCategories[c] }

// CategorySubtotal is one row of the per-category breakdown for a period.
type CategorySubtotal struct {
	Category string  `json:"category"`
	Total    float64 `json:"total"`
}

// NetSummary is the headline Financials roll-up for a period (cash-basis):
// Collected (REUSED from WO-F1's paid_cash aggregation) − Expenses = Net. Net may
// be negative. All figures are owner-scoped and Amman-bucketed.
type NetSummary struct {
	From       string             `json:"from"`
	To         string             `json:"to"`
	Collected  float64            `json:"collected"`
	Expenses   float64            `json:"expenses"`
	Net        float64            `json:"net"`
	ByCategory []CategorySubtotal `json:"by_category"`
	Series     []NetBucket        `json:"series"`
}

// NetBucket is one period bucket on the Net-over-time series: the F1 collected for
// the bucket, the expenses bucketed identically, and their difference.
type NetBucket struct {
	Bucket    string  `json:"bucket"`
	Collected float64 `json:"collected"`
	Expenses  float64 `json:"expenses"`
	Net       float64 `json:"net"`
}
