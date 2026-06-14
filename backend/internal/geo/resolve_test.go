package geo

import "testing"

func TestParseCoordinates(t *testing.T) {
	cases := []struct {
		name        string
		url         string
		wantOK      bool
		wantLat     float64
		wantLng     float64
	}{
		{
			name:    "!3d!4d canonical pin",
			url:     "https://www.google.com/maps/place/Pitch/@32.0123,35.8456,17z/data=!3m1!4b1!8m2!3d32.0150!4d35.8500",
			wantOK:  true,
			wantLat: 32.0150, wantLng: 35.8500, // !3d!4d wins over the @ viewport
		},
		{
			name:    "@ viewport only",
			url:     "https://www.google.com/maps/@32.0400,35.9100,15z",
			wantOK:  true,
			wantLat: 32.0400, wantLng: 35.9100,
		},
		{
			name:    "?q= query coordinate",
			url:     "https://maps.google.com/?q=32.0500,35.9200",
			wantOK:  true,
			wantLat: 32.0500, wantLng: 35.9200,
		},
		{
			name:   "consent interstitial / garbage → no match",
			url:    "https://consent.google.com/m?continue=https://maps.google.com&gl=JO",
			wantOK: false,
		},
		{
			name:   "plain text, no coordinates",
			url:    "https://maps.app.goo.gl/abc123XYZ",
			wantOK: false,
		},
	}
	for _, tc := range cases {
		lat, lng, ok := ParseCoordinates(tc.url)
		if ok != tc.wantOK {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.wantOK)
			continue
		}
		if ok && (lat != tc.wantLat || lng != tc.wantLng) {
			t.Errorf("%s: got (%.4f,%.4f), want (%.4f,%.4f)", tc.name, lat, lng, tc.wantLat, tc.wantLng)
		}
	}
}

func TestValidateJordanCoords(t *testing.T) {
	cases := []struct {
		name     string
		lat, lng float64
		want     bool
	}{
		{"Amman", 32.0150, 35.8500, true},
		{"Aqaba (south Jordan)", 29.53, 35.00, true},
		{"(0,0) sentinel", 0, 0, false},
		{"out of Jordan — London", 51.50, -0.12, false},
		{"out of Jordan — just south of box", 28.9, 36.0, false},
		{"out of Jordan — east of box", 31.0, 40.0, false},
		{"out of global range", 200, 500, false},
	}
	for _, tc := range cases {
		if got := ValidateJordanCoords(tc.lat, tc.lng); got != tc.want {
			t.Errorf("%s: ValidateJordanCoords(%.4f,%.4f) = %v, want %v", tc.name, tc.lat, tc.lng, got, tc.want)
		}
	}
}

// End-to-end of the pure pieces: an out-of-Jordan pin parses but is rejected by
// the validator (the resolver would return ok=false for it).
func TestParseThenValidate_OutOfJordanRejected(t *testing.T) {
	url := "https://www.google.com/maps/place/X/data=!3d48.8584!4d2.2945" // Eiffel Tower
	lat, lng, ok := ParseCoordinates(url)
	if !ok {
		t.Fatalf("parser should extract the coordinate")
	}
	if ValidateJordanCoords(lat, lng) {
		t.Fatalf("(%.4f,%.4f) is in Paris — must fail the Jordan box", lat, lng)
	}
}

func TestIsAllowedMapsHost(t *testing.T) {
	allowed := []string{"maps.app.goo.gl", "goo.gl", "google.com", "maps.google.com", "consent.google.com", "www.google.com"}
	for _, h := range allowed {
		if !isAllowedMapsHost(h) {
			t.Errorf("%s should be allowed", h)
		}
	}
	denied := []string{"evil.com", "google.com.attacker.net", "notgoogle.com", "localhost", "169.254.169.254"}
	for _, h := range denied {
		if isAllowedMapsHost(h) {
			t.Errorf("%s should be denied (SSRF guard)", h)
		}
	}
}
