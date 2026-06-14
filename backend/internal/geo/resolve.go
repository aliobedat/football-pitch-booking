package geo

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Google Maps URL → coordinate resolution.
//
// A pitch's maps_url is usually a short link (maps.app.goo.gl/…) that redirects to
// a canonical maps URL carrying the coordinates. We resolve it defensively:
//   - the PARSER (ParseCoordinates) is pure — it extracts a lat/lng from one URL
//     string by a fixed pattern priority, so it is fully unit-testable offline;
//   - the FETCHER (ResolvePitchCoordinates) follows redirects under a strict
//     Google-domain allowlist (SSRF guard) and runs the parser on every hop;
//   - the VALIDATOR (ValidateJordanCoords) rejects anything out of range, the
//     (0,0) sentinel, or outside Jordan's bounding box.
// ─────────────────────────────────────────────────────────────────────────────

// Jordan bounding box — coordinates outside this are rejected as bad data.
const (
	jordanMinLat = 29.0
	jordanMaxLat = 33.4
	jordanMinLng = 34.9
	jordanMaxLng = 39.3
)

// Pattern priority (highest first):
//  1. !3d<lat>!4d<lng>  — the canonical place pin in an expanded maps URL
//  2. @<lat>,<lng>      — the map viewport centre
//  3. ?q=/?ll=<lat>,<lng> — a query coordinate
var (
	re3d4d = regexp.MustCompile(`!3d(-?\d+\.?\d*)!4d(-?\d+\.?\d*)`)
	reAt   = regexp.MustCompile(`@(-?\d+\.\d+),(-?\d+\.\d+)`)
	reQ    = regexp.MustCompile(`[?&](?:q|ll)=(-?\d+\.\d+),(-?\d+\.\d+)`)
)

// ParseCoordinates extracts a lat/lng from a single URL string using the fixed
// pattern priority. It is PURE (no network, no validation) — it returns the first
// pattern that matches, leaving range/Jordan checks to ValidateJordanCoords. ok is
// false when no pattern matches or a captured number fails to parse.
func ParseCoordinates(rawURL string) (lat, lng float64, ok bool) {
	for _, re := range []*regexp.Regexp{re3d4d, reAt, reQ} {
		if m := re.FindStringSubmatch(rawURL); m != nil {
			la, err1 := strconv.ParseFloat(m[1], 64)
			ln, err2 := strconv.ParseFloat(m[2], 64)
			if err1 == nil && err2 == nil {
				return la, ln, true
			}
		}
	}
	return 0, 0, false
}

// ValidateJordanCoords reports whether (lat,lng) is a usable Jordan coordinate:
// in valid global range, NOT exactly (0,0), AND inside Jordan's bounding box.
func ValidateJordanCoords(lat, lng float64) bool {
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return false
	}
	if lat == 0 && lng == 0 {
		return false
	}
	return lat >= jordanMinLat && lat <= jordanMaxLat &&
		lng >= jordanMinLng && lng <= jordanMaxLng
}

// ValidGoogleMapsURL reports whether raw is a present, well-formed https URL on an
// allowed Google maps host — the "valid location link" half of RequireLocationSource.
func ValidGoogleMapsURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	return IsAllowedMapsHost(u.Hostname())
}

// LocationState is the location a pitch would have AFTER an edit: its resulting
// maps_url and its current coordinates.
type LocationState struct {
	MapsURL string
	Coords  Coordinates
}

// RequireLocationSource passes when the after-edit state has a usable location
// source: EITHER a valid Google maps_url (from which coordinates can be resolved)
// OR already-usable coordinates (protecting legacy pitches that carry coordinates
// but no link). It is the single mandatory-location gate for create + update.
func RequireLocationSource(s LocationState) bool {
	return ValidGoogleMapsURL(s.MapsURL) || s.Coords.HasUsableCoords()
}

// IsAllowedMapsHost gates redirect hops to Google's link/maps domains (SSRF guard).
func IsAllowedMapsHost(host string) bool {
	host = strings.ToLower(host)
	switch host {
	case "goo.gl", "maps.app.goo.gl", "google.com", "maps.google.com":
		return true
	}
	// Allow Google subdomains reached during redirects (e.g. consent.google.com),
	// and the goo.gl family — but nothing off the Google estate.
	return strings.HasSuffix(host, ".google.com") || strings.HasSuffix(host, ".goo.gl")
}

// mapsUserAgent is a realistic browser UA so Google returns the redirect rather
// than a bot-block page.
const mapsUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// ResolvePitchCoordinates follows a Google Maps share URL through its redirects
// (within the allowlist), scanning every hop for coordinates and validating the
// first match against the Jordan box. ok is false on any failure: a non-Google
// initial host, a redirect leaving the allowlist, a transport error, or no valid
// coordinate found.
func ResolvePitchCoordinates(ctx context.Context, mapsURL string) (lat, lng float64, ok bool) {
	mapsURL = strings.TrimSpace(mapsURL)
	if mapsURL == "" {
		return 0, 0, false
	}
	u, err := url.Parse(mapsURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return 0, 0, false
	}
	if !IsAllowedMapsHost(u.Hostname()) {
		return 0, 0, false // reject a non-allowed initial host
	}

	// Collect every hop URL (initial + each redirect target) for parsing.
	hops := []string{mapsURL}

	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !IsAllowedMapsHost(req.URL.Hostname()) {
				return fmt.Errorf("redirect to disallowed host %q", req.URL.Hostname())
			}
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			hops = append(hops, req.URL.String())
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mapsURL, nil)
	if err != nil {
		return 0, 0, false
	}
	req.Header.Set("User-Agent", mapsUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		// A redirect-rejection (allowlist) lands here — fail closed.
		return 0, 0, false
	}
	defer resp.Body.Close()
	if resp.Request != nil && resp.Request.URL != nil {
		hops = append(hops, resp.Request.URL.String()) // final URL
	}

	// Parse each hop in order; return the first coordinate that validates.
	for _, h := range hops {
		if la, ln, found := ParseCoordinates(h); found && ValidateJordanCoords(la, ln) {
			return la, ln, true
		}
	}
	return 0, 0, false
}
