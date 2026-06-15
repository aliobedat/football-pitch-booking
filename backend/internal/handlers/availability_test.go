package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParseHHMM(t *testing.T) {
	cases := []struct {
		in       string
		h, m     int
		ok       bool
	}{
		{"08:00", 8, 0, true},
		{"23:59", 23, 59, true},
		{"00:00", 0, 0, true},
		{"24:00", 0, 0, false},
		{"08:60", 0, 0, false},
		{"8:00", 0, 0, false},  // not zero-padded / wrong length
		{"0800", 0, 0, false},  // missing colon
		{"ab:cd", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, tc := range cases {
		h, m, ok := parseHHMM(tc.in)
		if ok != tc.ok || (ok && (h != tc.h || m != tc.m)) {
			t.Errorf("parseHHMM(%q) = (%d,%d,%v), want (%d,%d,%v)", tc.in, h, m, ok, tc.h, tc.m, tc.ok)
		}
	}
}

func TestParsePlayerCoords(t *testing.T) {
	mk := func(q string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/x?"+q, nil)
		return c
	}
	// Neither present → no location, valid.
	if coords, ok := parsePlayerCoords(mk("")); !ok || coords.HasUsableCoords() {
		t.Errorf("no coords: got ok=%v usable=%v, want ok=true usable=false", ok, coords.HasUsableCoords())
	}
	// Both present and valid.
	if coords, ok := parsePlayerCoords(mk("lat=32.0&lng=35.9")); !ok || !coords.HasUsableCoords() {
		t.Errorf("valid pair: got ok=%v usable=%v, want both true", ok, coords.HasUsableCoords())
	}
	// Exactly one present → invalid.
	if _, ok := parsePlayerCoords(mk("lat=32.0")); ok {
		t.Errorf("lat only: got ok=true, want false")
	}
	if _, ok := parsePlayerCoords(mk("lng=35.9")); ok {
		t.Errorf("lng only: got ok=true, want false")
	}
	// Non-numeric → invalid.
	if _, ok := parsePlayerCoords(mk("lat=abc&lng=35.9")); ok {
		t.Errorf("non-numeric lat: got ok=true, want false")
	}
}

// The static /pitches/availability route must coexist with the /pitches/:id param
// route (Gin matches the static segment first). This guards against a future Gin
// upgrade or route reorder reintroducing a registration panic / mis-dispatch.
func TestAvailabilityRouteCoexistsWithParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/pitches/availability", func(c *gin.Context) { c.String(http.StatusOK, "availability") })
	r.GET("/pitches/:id", func(c *gin.Context) { c.String(http.StatusOK, "id="+c.Param("id")) })

	check := func(path, want string) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK || w.Body.String() != want {
			t.Errorf("GET %s → (%d, %q), want (200, %q)", path, w.Code, w.Body.String(), want)
		}
	}
	check("/pitches/availability", "availability")
	check("/pitches/42", "id=42")
}
