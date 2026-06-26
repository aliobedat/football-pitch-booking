// Package phone centralises Malaeb's phone-number identity rule: normalisation to
// E.164 with a Jordan (+962) default. It is the SINGLE source of truth for turning
// user/owner-entered numbers into the canonical form used as a stable identifier —
// the OTP login path, staff binding, and the CRM customer dedup key all share it,
// so a number entered as "079…", "+96279…", or "0096279…" resolves identically
// everywhere. Extracted from the phone-auth handler (Cockpit WO1) so non-handler
// callers (the customer backfill job) reuse the exact same rule.
package phone

import (
	"errors"
	"regexp"
	"strings"

	"github.com/nyaruka/phonenumbers"
)

// ErrNotJOMobile is returned by ValidateJOMobile when a number is not a valid
// Jordanian mobile line. It is a distinct sentinel so the caller can map it to a
// precise 422 without conflating it with the looser E.164 normalisation error.
var ErrNotJOMobile = errors.New("phone number must be a valid Jordanian mobile number")

// DefaultCountryCode is prepended to local-format numbers that omit one. Malaeb
// launches in Jordan (+962); see CLAUDE.md (app-layer normalisation).
const DefaultCountryCode = "962"

// E164Regex mirrors the users_phone_e164_chk DB constraint: a leading '+', a
// non-zero country-code digit, then up to 14 more digits (15 total max).
var E164Regex = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

// Normalize converts a raw phone string to canonical E.164, applying the Jordan
// default country code to local-format inputs. It returns an error for an empty
// or structurally invalid number (the caller decides whether that is fatal — the
// backfill, for instance, SKIPS such rows rather than failing).
func Normalize(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("phone number is required")
	}

	// Drop common separators.
	replacer := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "")
	s = replacer.Replace(s)

	switch {
	case strings.HasPrefix(s, "+"):
		// already international
	case strings.HasPrefix(s, "00"):
		s = "+" + strings.TrimPrefix(s, "00")
	case strings.HasPrefix(s, "0"):
		s = "+" + DefaultCountryCode + strings.TrimPrefix(s, "0")
	default:
		s = "+" + DefaultCountryCode + s
	}

	if !E164Regex.MatchString(s) {
		return "", errors.New("phone number must be a valid E.164 number (e.g. +9627XXXXXXXX)")
	}
	return s, nil
}

// ValidateJOMobile enforces the STRICT Jordan-mobile rule used ONLY by the player
// booking flow (purpose="booking"). It is deliberately NOT applied to the shared
// /auth/request-otp login path, so owner/staff/CRM/admin identities keep the
// looser Normalize-only rule. Input must already be canonical E.164 (the caller
// runs Normalize first). It accepts a number only when libphonenumber, parsed in
// region JO, confirms it is (a) valid, (b) resolves to region JO, and (c) is a
// MOBILE or FIXED_LINE_OR_MOBILE line — landlines, short codes, and non-JO
// numbers are rejected with ErrNotJOMobile.
func ValidateJOMobile(e164 string) error {
	num, err := phonenumbers.Parse(strings.TrimSpace(e164), "JO")
	if err != nil {
		return ErrNotJOMobile
	}
	if !phonenumbers.IsValidNumber(num) {
		return ErrNotJOMobile
	}
	if phonenumbers.GetRegionCodeForNumber(num) != "JO" {
		return ErrNotJOMobile
	}
	switch phonenumbers.GetNumberType(num) {
	case phonenumbers.MOBILE, phonenumbers.FIXED_LINE_OR_MOBILE:
		return nil
	default:
		return ErrNotJOMobile
	}
}
