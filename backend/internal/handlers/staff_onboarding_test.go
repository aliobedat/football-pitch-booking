package handlers

// Handler-level onboarding tests (no DB): password validation + hashing wiring +
// the new error→HTTP mapping. The role-state matrix and the real SQL ensure flow
// are exercised by the DB-gated repository tests (staff_onboarding_db_test.go);
// the login round-trip by staff_login_roundtrip_test.go.

import (
	"net/http"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/ali/football-pitch-api/internal/repository"
)

// onboardBody builds an invite payload with full_name + password.
func onboardBody(phone, fullName, password string, pitchIDs ...int) map[string]any {
	return map[string]any{
		"phone": phone, "full_name": fullName, "password": password, "pitch_ids": pitchIDs,
	}
}

// TestInviteStaff_HashesPassword: a provided password reaches the repo as a bcrypt
// hash (never plaintext), and full_name is forwarded.
func TestInviteStaff_HashesPassword(t *testing.T) {
	repo := &fakeStaffRepo{}
	r := newStaffRouter(NewStaffHandler(repo, bcrypt.MinCost), 42, "owner")
	rec := doJSON(t, r, http.MethodPost, "/owner/staff",
		onboardBody("0791234567", "حارس الملعب", "super-secret", 7))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if repo.lastProv.PasswordHash == "" {
		t.Fatal("password not forwarded to repo")
	}
	if repo.lastProv.PasswordHash == "super-secret" {
		t.Fatal("plaintext password forwarded; must be hashed")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(repo.lastProv.PasswordHash), []byte("super-secret")); err != nil {
		t.Fatalf("forwarded value is not a bcrypt hash of the password: %v", err)
	}
	if repo.lastProv.FullName != "حارس الملعب" {
		t.Fatalf("full_name = %q, want forwarded", repo.lastProv.FullName)
	}
}

// TestInviteStaff_WeakPassword_422: a too-short password is rejected before the
// repo runs.
func TestInviteStaff_WeakPassword_422(t *testing.T) {
	repo := &fakeStaffRepo{}
	r := newStaffRouter(NewStaffHandler(repo, bcrypt.MinCost), 42, "owner")
	rec := doJSON(t, r, http.MethodPost, "/owner/staff",
		onboardBody("0791234567", "X", "short", 7))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for a weak password; body=%s", rec.Code, rec.Body.String())
	}
	if repo.createCalls != 0 {
		t.Fatal("repo ran despite a weak password; must reject first")
	}
}

// TestInviteStaff_NoPassword_EmptyProv: with no password the handler forwards an
// empty hash (the repo decides whether this case requires one).
func TestInviteStaff_NoPassword_EmptyProv(t *testing.T) {
	repo := &fakeStaffRepo{}
	r := newStaffRouter(NewStaffHandler(repo, bcrypt.MinCost), 42, "owner")
	rec := doJSON(t, r, http.MethodPost, "/owner/staff", inviteBody("0791234567", 7))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if repo.lastProv.PasswordHash != "" {
		t.Fatalf("expected empty password hash when none provided, got %q", repo.lastProv.PasswordHash)
	}
}

// TestInviteStaff_PasswordRequired_422: the repo's ErrPasswordRequired maps to 422
// with the password_required code.
func TestInviteStaff_PasswordRequired_422(t *testing.T) {
	repo := &fakeStaffRepo{createErr: repository.ErrPasswordRequired}
	r := newStaffRouter(NewStaffHandler(repo, bcrypt.MinCost), 42, "owner")
	rec := doJSON(t, r, http.MethodPost, "/owner/staff", inviteBody("0791234567", 7))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}
