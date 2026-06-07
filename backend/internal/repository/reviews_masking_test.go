package repository

import "testing"

// TestMaskReviewerName locks the PII-masking contract: the first token is kept
// whole, every later token collapses to its first RUNE plus ".", and Arabic/RTL
// text is split by code point (never mid-byte). Empty/whitespace → "".
func TestMaskReviewerName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"latin two tokens", "Ahmad Khaled", "Ahmad K."},
		{"latin three tokens", "Ahmad Khaled Saleh", "Ahmad K. S."},
		{"arabic two tokens", "أحمد خالد", "أحمد خ."},
		{"arabic three tokens", "أحمد خالد صالح", "أحمد خ. ص."},
		{"single token latin", "Ahmad", "Ahmad"},
		{"single token arabic", "أحمد", "أحمد"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"collapses inner whitespace", "Ahmad    Khaled", "Ahmad K."},
		{"trims surrounding whitespace", "  Ahmad Khaled  ", "Ahmad K."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := maskReviewerName(tc.in)
			if got != tc.want {
				t.Fatalf("maskReviewerName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
