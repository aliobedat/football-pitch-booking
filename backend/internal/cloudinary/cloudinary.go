// Package cloudinary wraps the backend's interaction with Cloudinary for the
// backend-signed DIRECT-UPLOAD flow used by pitch images:
//
//	browser → backend (sign upload params) → Cloudinary (browser uploads bytes)
//	        → backend (persist secure_url + public_id, destroy replaced asset)
//
// File bytes NEVER pass through the backend. The API SECRET lives only here (and
// the process env) and is never serialised to a client: it is used solely to
// (a) sign upload parameters and (b) destroy replaced assets.
package cloudinary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cloudinary/cloudinary-go/v2"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"

	"github.com/ali/football-pitch-api/internal/config"
)

// deliveryHost is the host Cloudinary serves delivery URLs (secure_url) from.
// Every asset URL is https://res.cloudinary.com/<cloud_name>/... — the persist
// guard pins both this host and our cloud name so a client can never persist an
// arbitrary external URL (SSRF / stored-content abuse).
const deliveryHost = "res.cloudinary.com"

// Service signs upload params and destroys replaced assets for one Cloudinary
// account. It is safe for concurrent use.
type Service struct {
	cld          *cloudinary.Cloudinary
	cloudName    string
	apiKey       string
	apiSecret    string
	uploadPreset string
	folder       string
}

// New builds a Service from the validated Cloudinary config. It returns an error
// only if the SDK client cannot be constructed from the credentials.
func New(cfg config.CloudinaryConfig) (*Service, error) {
	cld, err := cloudinary.NewFromParams(cfg.CloudName, cfg.APIKey, cfg.APISecret)
	if err != nil {
		return nil, fmt.Errorf("cloudinary: init client: %w", err)
	}
	return &Service{
		cld:          cld,
		cloudName:    cfg.CloudName,
		apiKey:       cfg.APIKey,
		apiSecret:    cfg.APISecret,
		uploadPreset: cfg.UploadPreset,
		folder:       cfg.Folder,
	}, nil
}

// SignedUpload is the non-secret payload the browser needs to upload directly to
// Cloudinary. It deliberately carries NO api_secret. folder and upload_preset are
// pinned into the signature, so a leaked payload cannot target a different folder
// or preset (any change invalidates the signature).
type SignedUpload struct {
	Timestamp    int64  `json:"timestamp"`
	Signature    string `json:"signature"`
	APIKey       string `json:"api_key"`
	CloudName    string `json:"cloud_name"`
	Folder       string `json:"folder"`
	UploadPreset string `json:"upload_preset"`
}

// SignUpload produces a fresh signed payload for a single direct upload.
//
// The signature is SHA-256 of the to-sign params sorted by key as `k=v` joined
// with `&`, with the API secret appended. The signed params are exactly
// {folder, timestamp, upload_preset}; the browser must send those same values
// plus the file and api_key, so the upload is bound to our pinned folder +
// preset.
//
// NOTE: the hash algorithm is NOT a signed parameter — Cloudinary determines it
// from the account's configured "Signature algorithm" setting, which MUST be set
// to SHA-256 for these signatures to verify (see docs/PR_pitch_image_upload.md).
func (s *Service) SignUpload() SignedUpload {
	ts := time.Now().Unix()

	params := url.Values{}
	params.Set("folder", s.folder)
	params.Set("timestamp", strconv.FormatInt(ts, 10))
	params.Set("upload_preset", s.uploadPreset)

	return SignedUpload{
		Timestamp:    ts,
		Signature:    signParams(params, s.apiSecret),
		APIKey:       s.apiKey,
		CloudName:    s.cloudName,
		Folder:       s.folder,
		UploadPreset: s.uploadPreset,
	}
}

// OwnsURL reports whether rawURL is a delivery URL for THIS Cloudinary account:
// https scheme, host res.cloudinary.com, and a path whose first segment is our
// cloud name. This is the real validation boundary on the persist path — bytes
// never reach the backend, so an attacker's only lever is the URL string, and a
// non-matching URL must be rejected (no arbitrary external URLs persisted).
func (s *Service) OwnsURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	if u.Scheme != "https" || u.Host != deliveryHost {
		return false
	}
	// Path is /<cloud_name>/<resource_type>/... — first non-empty segment must
	// be our cloud name.
	first, _, _ := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
	return first == s.cloudName
}

// Destroy removes a previously uploaded asset by public_id. It is best-effort:
// callers use it to clean up a replaced image and should log — not fail the
// request — on error, since the new image is already persisted. A blank publicID
// is a no-op.
func (s *Service) Destroy(ctx context.Context, publicID string) error {
	if strings.TrimSpace(publicID) == "" {
		return nil
	}
	res, err := s.cld.Upload.Destroy(ctx, uploader.DestroyParams{PublicID: publicID})
	if err != nil {
		return fmt.Errorf("cloudinary: destroy %q: %w", publicID, err)
	}
	// The Destroy API returns 200 with {"result":"not found"} rather than an HTTP
	// error when the asset is already gone — treat only "ok" as a real deletion.
	if res != nil && res.Result != "" && res.Result != "ok" && res.Result != "not found" {
		return fmt.Errorf("cloudinary: destroy %q: result=%q", publicID, res.Result)
	}
	return nil
}

// signParams computes a Cloudinary upload signature: sha256(sorted "k=v" joined
// by "&" + secret), hex-encoded. The account's "Signature algorithm" must be set
// to SHA-256 to match.
func signParams(params url.Values, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params.Get(k))
	}
	toSign := strings.Join(parts, "&") + secret

	sum := sha256.Sum256([]byte(toSign))
	return hex.EncodeToString(sum[:])
}
