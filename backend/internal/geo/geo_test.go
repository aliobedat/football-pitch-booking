package geo

import (
	"math"
	"testing"
)

func ptr(f float64) *float64 { return &f }

func TestHasUsableCoords(t *testing.T) {
	cases := []struct {
		name string
		c    Coordinates
		want bool
	}{
		{"both nil", Coordinates{}, false},
		{"lat nil", Coordinates{Lng: ptr(35.9)}, false},
		{"lng nil", Coordinates{Lat: ptr(32.0)}, false},
		{"exactly (0,0) sentinel", Coordinates{Lat: ptr(0), Lng: ptr(0)}, false},
		{"real Amman coords", Coordinates{Lat: ptr(32.0), Lng: ptr(35.9)}, true},
		{"lat 0 but lng non-zero", Coordinates{Lat: ptr(0), Lng: ptr(35.9)}, true},
		{"negative coords", Coordinates{Lat: ptr(-1.0), Lng: ptr(-1.0)}, true},
	}
	for _, tc := range cases {
		if got := tc.c.HasUsableCoords(); got != tc.want {
			t.Errorf("%s: HasUsableCoords() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestHaversineKm(t *testing.T) {
	// Same point → 0.
	if d := HaversineKm(32.0, 35.9, 32.0, 35.9); d != 0 {
		t.Errorf("same point distance = %v, want 0", d)
	}
	// ~0.1° of latitude ≈ 11.1 km near this latitude.
	d := HaversineKm(32.0, 35.9, 32.1, 35.9)
	if math.Abs(d-11.1) > 0.3 {
		t.Errorf("0.1° lat distance = %.3f km, want ≈11.1", d)
	}
	// Monotonic: farther apart → larger.
	near := HaversineKm(32.0, 35.9, 32.001, 35.901)
	far := HaversineKm(32.0, 35.9, 32.5, 36.4)
	if near >= far {
		t.Errorf("near %.3f should be < far %.3f", near, far)
	}
}
