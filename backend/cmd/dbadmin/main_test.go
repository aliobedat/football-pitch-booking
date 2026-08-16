package main

// WO-DBADMIN-CREATE-OWNER: proves the 23505 (unique_violation) path on
// -create-owner's INSERT produces the SAME clean "phone already exists"
// message the pre-check path uses, instead of surfacing the raw pg driver
// error — the fix for the TOCTOU race between the count-check and the INSERT.

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestCreateOwnerFriendlyError_UniqueViolation(t *testing.T) {
	pgErr := &pgconn.PgError{
		Code:    "23505",
		Message: `duplicate key value violates unique constraint "idx_users_phone_unique"`,
	}
	// Simulate how pgx surfaces it: wrapped, not returned bare.
	wrapped := fmt.Errorf("insert: %w", pgErr)

	msg, ok := createOwnerFriendlyError(wrapped, "+962795303606")
	if !ok {
		t.Fatalf("createOwnerFriendlyError: ok=false, want true for a 23505 error")
	}
	want := "refusing: a user already exists for phone +962795303606"
	if msg != want {
		t.Fatalf("message=%q want %q", msg, want)
	}
	// The raw pg error text must NOT leak into the friendly message.
	if errors.Is(wrapped, pgErr) == false {
		t.Fatalf("test setup broken: wrapped error does not unwrap to pgErr")
	}
}

func TestCreateOwnerFriendlyError_OtherError(t *testing.T) {
	other := errors.New("connection reset by peer")
	msg, ok := createOwnerFriendlyError(other, "+962795303606")
	if ok {
		t.Fatalf("createOwnerFriendlyError: ok=true, want false for a non-23505 error (got msg=%q)", msg)
	}
	if msg != "" {
		t.Fatalf("msg=%q want empty when ok=false", msg)
	}
}

func TestCreateOwnerFriendlyError_DifferentPgCode(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23502", Message: "null value in column violates not-null constraint"}
	msg, ok := createOwnerFriendlyError(fmt.Errorf("insert: %w", pgErr), "+962795303606")
	if ok {
		t.Fatalf("createOwnerFriendlyError: ok=true for code 23502, want false (only 23505 is mapped); msg=%q", msg)
	}
}
