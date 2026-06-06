package cloudinary

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strconv"
	"testing"

	"github.com/ali/football-pitch-api/internal/config"
)

func testService(t *testing.T) *Service {
	t.Helper()
	svc, err := New(config.CloudinaryConfig{
		CloudName:    "malaeb-cloud",
		APIKey:       "123456789",
		APISecret:    "topsecret",
		UploadPreset: "malaeb_pitches",
		Folder:       "malaeb/pitches",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

// signParams must match Cloudinary's SHA-256 signature exactly:
// sha256(sorted "k=v" joined by "&" + secret).
func TestSignParams_KnownVector(t *testing.T) {
	params := url.Values{}
	params.Set("folder", "malaeb/pitches")
	params.Set("timestamp", "1700000000")
	params.Set("upload_preset", "malaeb_pitches")

	want := func() string {
		raw := "folder=malaeb/pitches&timestamp=1700000000&upload_preset=malaeb_pitches" + "topsecret"
		sum := sha256.Sum256([]byte(raw))
		return hex.EncodeToString(sum[:])
	}()

	if got := signParams(params, "topsecret"); got != want {
		t.Fatalf("signParams = %q, want %q", got, want)
	}
}

// The signed payload must carry the pinned folder/preset and the non-secret
// api_key + cloud_name — and the signature must verify against those exact params.
// The API SECRET must never appear anywhere in the payload.
func TestSignUpload_PayloadAndSecretLeak(t *testing.T) {
	svc := testService(t)
	p := svc.SignUpload()

	if p.Folder != "malaeb/pitches" || p.UploadPreset != "malaeb_pitches" {
		t.Fatalf("pinned folder/preset wrong: %+v", p)
	}
	if p.APIKey != "123456789" || p.CloudName != "malaeb-cloud" {
		t.Fatalf("non-secret fields wrong: %+v", p)
	}
	if p.Timestamp == 0 || p.Signature == "" {
		t.Fatalf("missing timestamp/signature: %+v", p)
	}

	// Recompute the signature over exactly the signed params and compare.
	params := url.Values{}
	params.Set("folder", p.Folder)
	params.Set("timestamp", strconv.FormatInt(p.Timestamp, 10))
	params.Set("upload_preset", p.UploadPreset)
	if want := signParams(params, "topsecret"); want != p.Signature {
		t.Fatalf("signature %q does not verify (want %q)", p.Signature, want)
	}

	// Secret-leak guard: serialise every field and assert the secret is absent.
	for _, field := range []string{p.Signature, p.APIKey, p.CloudName, p.Folder, p.UploadPreset, strconv.FormatInt(p.Timestamp, 10)} {
		if field == "topsecret" {
			t.Fatalf("API secret leaked into signed payload field %q", field)
		}
	}
}

func TestOwnsURL(t *testing.T) {
	svc := testService(t)
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"our cloud https", "https://res.cloudinary.com/malaeb-cloud/image/upload/v1/malaeb/pitches/a.webp", true},
		{"wrong cloud name", "https://res.cloudinary.com/someone-else/image/upload/v1/x.webp", false},
		{"http not https", "http://res.cloudinary.com/malaeb-cloud/image/upload/v1/x.webp", false},
		{"foreign host", "https://evil.example.com/malaeb-cloud/image/upload/x.webp", false},
		{"host smuggled in path", "https://attacker.com/res.cloudinary.com/malaeb-cloud/x.webp", false},
		{"empty", "", false},
		{"not a url", "::::", false},
		{"cloud name as substring only", "https://res.cloudinary.com/malaeb-cloud-evil/x.webp", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := svc.OwnsURL(c.url); got != c.want {
				t.Fatalf("OwnsURL(%q) = %v, want %v", c.url, got, c.want)
			}
		})
	}
}
