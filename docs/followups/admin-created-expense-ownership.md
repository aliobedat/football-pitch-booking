# Design question: admin-created expense ownership attribution

`expense_repository.go:147` has an idempotency lookup scoped by `owner_id`. This
should not be mechanically changed in the staff-permissions slice. We need a
product decision for what `owner_id` an admin-created expense should carry before
touching expense logic.

---

Status: open — documentation only, no code change.
Out of scope for `fix/staff-admin-permissions` (staff handler/repository).
Raised from the ownership-predicate grep during that slice; expense/financial
logic is explicitly out of that slice's scope.
