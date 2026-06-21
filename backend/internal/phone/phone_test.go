package phone

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"+962791234567", "+962791234567", false},    // already international
		{"0791234567", "+962791234567", false},       // local leading 0 → +962
		{"00962791234567", "+962791234567", false},   // 00 international prefix
		{"791234567", "+962791234567", false},        // bare local
		{"+962 79 123 4567", "+962791234567", false}, // separators stripped
		{"+962-79-123-4567", "+962791234567", false},
		{"", "", true},      // empty
		{"   ", "", true},   // blank
		{"abc", "", true},   // non-numeric
		{"+0123", "", true}, // leading country digit 0 is invalid E.164
	}
	for _, c := range cases {
		got, err := Normalize(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("Normalize(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Normalize(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
