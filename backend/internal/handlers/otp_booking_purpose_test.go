package handlers

// Delta D — strict Jordan-mobile validation is scoped to the player BOOKING flow
// (purpose="booking"). These tests prove the gate fires ONLY for that purpose and
// that the shared owner/staff login path (no purpose) is byte-for-byte unchanged
// — a JO landline that the booking gate rejects still sails through the default
// path.

import (
	"net/http"
	"testing"
)

// TestRequestOTP_BookingPurpose_ValidJOMobile_Accepted: a valid JO mobile with
// purpose="booking" passes the strict gate and reaches OTP dispatch (200).
func TestRequestOTP_BookingPurpose_ValidJOMobile_Accepted(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, http.MethodPost, "/auth/request-otp",
		map[string]any{"phone": "0791234567", "opt_in": true, "purpose": "booking"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRequestOTP_BookingPurpose_Landline_Rejected: a JO landline is rejected at
// the handler boundary (422) before any OTP is generated.
func TestRequestOTP_BookingPurpose_Landline_Rejected(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, http.MethodPost, "/auth/request-otp",
		map[string]any{"phone": "+96265001234", "opt_in": true, "purpose": "booking"}, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := h.fake.Last(); ok {
		t.Error("an OTP was dispatched for a rejected landline; want none")
	}
}

// TestRequestOTP_BookingPurpose_NonJO_Rejected: a valid non-JO number is rejected.
func TestRequestOTP_BookingPurpose_NonJO_Rejected(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, http.MethodPost, "/auth/request-otp",
		map[string]any{"phone": "+14155552671", "opt_in": true, "purpose": "booking"}, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRequestOTP_BookingPurpose_Invalid_Rejected: garbage input is rejected.
func TestRequestOTP_BookingPurpose_Invalid_Rejected(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, http.MethodPost, "/auth/request-otp",
		map[string]any{"phone": "+96200", "opt_in": true, "purpose": "booking"}, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRequestOTP_DefaultPurpose_LandlineNotRejectedByJORule: the SAME landline,
// WITHOUT purpose="booking", must NOT be rejected by the JO-mobile rule — the
// shared login path keeps the looser Normalize-only behaviour and reaches OTP
// dispatch (200). This is the regression guard for owner/staff/admin login.
func TestRequestOTP_DefaultPurpose_LandlineNotRejectedByJORule(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, http.MethodPost, "/auth/request-otp",
		map[string]any{"phone": "+96265001234", "opt_in": true}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (login path unchanged); body=%s", rec.Code, rec.Body.String())
	}
}

// TestRequestOTP_NonBookingPurpose_NonJONotRejectedByJORule: an explicit non-
// booking purpose is also exempt from the JO-mobile rule.
func TestRequestOTP_NonBookingPurpose_NonJONotRejectedByJORule(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, http.MethodPost, "/auth/request-otp",
		map[string]any{"phone": "+14155552671", "opt_in": true, "purpose": "login"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (non-booking purpose exempt); body=%s", rec.Code, rec.Body.String())
	}
}
