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
)

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
