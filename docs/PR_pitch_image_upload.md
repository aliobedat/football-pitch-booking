# Pitch image upload — backend-signed Cloudinary direct upload

Replaces the plain "رابط الصورة" URL text input on the pitch add/edit form with a
drag-and-drop dropzone that uploads images directly to Cloudinary, signed by the
Go backend. **File bytes never pass through the Go backend.** The Cloudinary API
**secret lives only in the backend env** and is never sent to the client.

## Flow

```
browser  ──(1) POST /pitches/upload-signature (CSRF)──▶  Go backend  (signs params with API secret)
browser  ──(2) POST file + signed fields ─────────────▶  Cloudinary  (direct upload, no app cookies)
browser  ──(3) PATCH /pitches/:id/image {url,public_id}▶ Go backend  (origin guard → persist → destroy old)
```

Create mode folds `image_url` + `image_public_id` into the `POST /pitches` payload
instead of step 3. Edit mode persists immediately on upload via `PATCH /pitches/:id/image`.

## Operator setup

### 1. Environment variables (Go backend) — ALL REQUIRED, fail-fast on boot

| Var | Secret? | Notes |
|-----|---------|-------|
| `CLOUDINARY_CLOUD_NAME`    | no  | non-secret, reaches the browser in the signed payload |
| `CLOUDINARY_API_KEY`       | no  | non-secret, reaches the browser |
| `CLOUDINARY_API_SECRET`    | **YES** | backend-only; used to sign uploads and destroy assets |
| `CLOUDINARY_UPLOAD_PRESET` | no  | optional, default `malaeb_pitches` |
| `CLOUDINARY_UPLOAD_FOLDER` | no  | optional, default `malaeb/pitches` |

The service **panics on boot** if any of the three credentials is missing
(`config.loadCloudinaryConfig`, consistent with the JWT/OTP startup assertions).

### 2. Cloudinary signed upload preset

Create a **signed** upload preset named **`malaeb_pitches`** in the Cloudinary
dashboard (Settings → Upload → Upload presets):

- **Signing mode:** Signed
- **Folder:** `malaeb/pitches`
- **Allowed formats:** `jpg, png, webp, heic`
- **Max file size:** ~8 MB (8388608 bytes)
- **Incoming transformation:** limit longest side to ~1600px + `quality:auto` +
  `format:auto` (delivers WebP). e.g. `c_limit,w_1600,h_1600/q_auto/f_auto`
- **Unique filename:** on

> **Signature algorithm:** the backend signs uploads with **SHA-256**. Set the
> account's **Settings → Security → Signature algorithm** to **SHA-256** (the
> hash algorithm is not sent as a request param — it must match account-side, or
> uploads fail with "Invalid Signature").

The folder and preset are **pinned into the signature** server-side, so a leaked
signature cannot redirect an upload to a different folder or preset.

### 3. Database migration (run once, manual psql per project convention)

```
psql "$DATABASE_URL" -f backend/migrations/009_pitch_image_public_id.up.sql
```

Adds `pitches.image_public_id TEXT NOT NULL DEFAULT ''` (the destroy handle for
old-asset cleanup). `image_url` already existed (migration 002).

## Security boundaries

- **Trust guard (the real validation boundary):** `PATCH /pitches/:id/image` and
  `POST /pitches` reject any `image_url` that is not `https://res.cloudinary.com/<our cloud_name>/…`.
  Since bytes never reach Go, the URL string is the only attacker-controllable
  input — an arbitrary external URL can never be persisted (SSRF / stored-content abuse).
- **AuthZ:** both endpoints are `RequireRole("owner","admin")` (players → 403) and
  CSRF-protected (cookie double-submit). The persist endpoint is actor-scoped in
  SQL: owner → own pitch only (else 404), admin → any, `deleted_at IS NULL`.
- **Old-asset cleanup:** replacing/removing an image best-effort `destroy`s the
  previous `public_id` (logged, never fails the request — the new image is already
  persisted).

## Endpoints

- `POST /api/v1/pitches/upload-signature` → `{ timestamp, signature, api_key, cloud_name, folder, upload_preset }`
- `PATCH /api/v1/pitches/:id/image` body `{ image_url, public_id }` (both empty = clear image)
- `POST /api/v1/pitches` now also accepts optional `image_url` + `image_public_id`

## Tests

- `internal/cloudinary/cloudinary_test.go` — signature vector, payload shape +
  **API-secret-leak guard**, `OwnsURL` accept/reject matrix.
- `internal/handlers/pitch_image_test.go` — signature endpoint owner/admin 200 &
  no secret in response, player → 403, foreign/incoherent URL → 400 (pre-DB).
- `internal/data/pitches_scoping_test.go` — persist scoping (owner own / foreign
  404 / soft-deleted 404), replace returns old public_id, clear (live-DB, skipped
  without `PITCH_SCOPING_TEST_DATABASE_URL`).
