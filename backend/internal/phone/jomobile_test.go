package phone

// Gate 3 — strict Jordan MSISDN validation (player BOOKING flow only). These
// tests pin ValidateJOMobile to: accept JO mobile in E.164 and local form
// (post-Normalize), reject JO landlines, reject non-JO numbers, and reject
// structurally invalid input. The shared login Normalize rule is unaffected.

import (
	"errors"
	"testing"
)

// TestMSISDN_JO_MobileValid accepts a canonical Jordanian mobile (+96279…).
func TestMSISDN_JO_MobileValid(t *testing.T) {
	for _, e164 := range []string{
		"+962790000001",
		"+962791234567",
		"+962770000000",
		"+962780000000",
	} {
		if err := ValidateJOMobile(e164); err != nil {
			t.Errorf("ValidateJOMobile(%q) = %v, want nil", e164, err)
		}
	}
}

// TestMSISDN_JO_LocalFormat_NormalizedToE164 proves the booking pipeline order:
// Normalize a local "07…" number to E.164, then ValidateJOMobile accepts it.
func TestMSISDN_JO_LocalFormat_NormalizedToE164(t *testing.T) {
	e164, err := Normalize("0791234567")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if e164 != "+962791234567" {
		t.Fatalf("Normalize = %q, want +962791234567", e164)
	}
	if err := ValidateJOMobile(e164); err != nil {
		t.Errorf("ValidateJOMobile(%q) = %v, want nil", e164, err)
	}
}

// TestMSISDN_JO_Landline_Rejected rejects a Jordanian fixed-line (Amman 06…),
// which is not a mobile line.
func TestMSISDN_JO_Landline_Rejected(t *testing.T) {
	for _, e164 := range []string{
		"+96265001234", // Amman landline
		"+96264000000",
	} {
		if err := ValidateJOMobile(e164); !errors.Is(err, ErrNotJOMobile) {
			t.Errorf("ValidateJOMobile(%q) = %v, want ErrNotJOMobile", e164, err)
		}
	}
}

// TestMSISDN_NonJO_Rejected rejects a valid number that belongs to another region.
func TestMSISDN_NonJO_Rejected(t *testing.T) {
	for _, e164 := range []string{
		"+14155552671", // US
		"+447911123456", // UK mobile
		"+966500000000", // Saudi mobile
	} {
		if err := ValidateJOMobile(e164); !errors.Is(err, ErrNotJOMobile) {
			t.Errorf("ValidateJOMobile(%q) = %v, want ErrNotJOMobile", e164, err)
		}
	}
}

// TestMSISDN_Invalid_Rejected rejects structurally invalid / unparseable input.
func TestMSISDN_Invalid_Rejected(t *testing.T) {
	for _, in := range []string{
		"",
		"+96200",
		"not-a-number",
		"+9627",       // too short
		"12345",
	} {
		if err := ValidateJOMobile(in); !errors.Is(err, ErrNotJOMobile) {
			t.Errorf("ValidateJOMobile(%q) = %v, want ErrNotJOMobile", in, err)
		}
	}
}
