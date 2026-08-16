package handlers

// WO-BOOKING-EDIT-CONTACT — Gate 1: DB-backed end-to-end tests for
// GET/PATCH /bookings/:id/contact, driving the REAL gin handlers +
// repositories + SQL against a live database. Gated on
// PITCH_SCOPING_TEST_DATABASE_URL; a skipped run is a failed gate.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run BookingContactDB -v

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/timeutil"
)

// setGuestPhone stamps guest_phone directly (mkBooking has no phone param;
// this WO is the first caller that needs a non-NULL guest_phone at seed time).
func (e *bsEnv) setGuestPhone(t *testing.T, id int64, phone string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE bookings SET guest_phone = $1 WHERE id = $2`, phone, id); err != nil {
		t.Fatalf("setGuestPhone: %v", err)
	}
}

type contactState struct {
	guestName, guestPhone, contactName, contactPhone *string
}

func (e *bsEnv) readContact(t *testing.T, id int64) contactState {
	t.Helper()
	var s contactState
	if err := e.pool.QueryRow(context.Background(),
		`SELECT guest_name, guest_phone, contact_name, contact_phone FROM bookings WHERE id=$1`, id).
		Scan(&s.guestName, &s.guestPhone, &s.contactName, &s.contactPhone); err != nil {
		t.Fatalf("readContact: %v", err)
	}
	return s
}

func decodeContact(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	return resp.Data
}

func TestBookingContactDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := newBSEnv(t)
	ownerA := e.router(e.ownerA, "owner", 0)
	adminR := e.router(0, "admin", 0)
	staffR := e.router(e.staffUser, "staff", e.pitchStaff)

	// ── T1: owner edits own booking → 200, persisted, phone normalized ──
	t.Run("owner_edit_own_ok", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		e.setGuestPhone(t, id, "+962700000000")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", id), gin.H{
			"guest_name": "احمد", "guest_phone": "0781234567",
		})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		st := e.readContact(t, id)
		if st.guestName == nil || *st.guestName != "احمد" {
			t.Fatalf("guest_name=%v want احمد", st.guestName)
		}
		if st.guestPhone == nil || *st.guestPhone != "+962781234567" {
			t.Fatalf("guest_phone=%v want +962781234567", st.guestPhone)
		}
	})

	// ── T2: admin edits any booking → 200 ──
	t.Run("admin_edit_any_ok", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchB, "academy", "confirmed", nil, s, en, 25, nil, "unpaid")
		rec := bsDo(adminR, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", id), gin.H{"guest_name": "سارة"})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		st := e.readContact(t, id)
		if st.guestName == nil || *st.guestName != "سارة" {
			t.Fatalf("guest_name=%v want سارة", st.guestName)
		}
	})

	// ── T3: staff PATCH → 403 (route-guard bar, mirroring extend) ──
	t.Run("staff_patch_403", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchStaff, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		rec := bsDo(staffR, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", id), gin.H{"guest_name": "x"})
		if rec.Code != 403 {
			t.Fatalf("status=%d want 403 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── T4: owner edits ANOTHER owner's booking → 404, row unchanged ──
	t.Run("cross_tenant_404_unchanged", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchB, "manual", "confirmed", nil, s, en, 25, nil, "unpaid") // owner B's pitch
		before := e.readContact(t, id)
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", id), gin.H{"guest_name": "hacked"})
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		after := e.readContact(t, id)
		if *before.guestName != *after.guestName {
			t.Fatalf("row changed on cross-tenant 404: before=%v after=%v", *before.guestName, *after.guestName)
		}
	})

	// ── T5: source='player' → 404, contact_name/contact_phone unchanged ──
	t.Run("player_source_404_unchanged", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 25, nil, "unpaid")
		before := e.readContact(t, id)
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", id), gin.H{"guest_name": "hacked"})
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		after := e.readContact(t, id)
		beforeCN, afterCN := "", ""
		if before.contactName != nil {
			beforeCN = *before.contactName
		}
		if after.contactName != nil {
			afterCN = *after.contactName
		}
		if beforeCN != afterCN {
			t.Fatalf("contact_name changed: before=%q after=%q", beforeCN, afterCN)
		}
	})

	// ── T6: source='block' → 404 ──
	t.Run("block_source_404", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "block", "confirmed", nil, s, en, 0, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", id), gin.H{"guest_name": "x"})
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── T7: status='cancelled' → 404 ──
	t.Run("cancelled_404", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "cancelled", nil, s, en, 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", id), gin.H{"guest_name": "x"})
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── T8: guest_name "   " → 422, row unchanged ──
	t.Run("blank_name_422_unchanged", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		before := e.readContact(t, id)
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", id), gin.H{"guest_name": "   "})
		if rec.Code != 422 {
			t.Fatalf("status=%d want 422 invalid_name (body=%s)", rec.Code, rec.Body.String())
		}
		after := e.readContact(t, id)
		if *before.guestName != *after.guestName {
			t.Fatalf("row changed on 422: before=%v after=%v", *before.guestName, *after.guestName)
		}
	})

	// ── T9: guest_phone "0781234567" → 200, stored as +962781234567 ──
	t.Run("phone_local_format_normalized", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", id), gin.H{"guest_phone": "0781234567"})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		st := e.readContact(t, id)
		if st.guestPhone == nil || *st.guestPhone != "+962781234567" {
			t.Fatalf("guest_phone=%v want +962781234567", st.guestPhone)
		}
	})

	// ── T10: guest_phone "abc" → 422, row unchanged ──
	t.Run("invalid_phone_422_unchanged", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		e.setGuestPhone(t, id, "+962700000001")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", id), gin.H{"guest_phone": "abc"})
		if rec.Code != 422 {
			t.Fatalf("status=%d want 422 invalid_phone (body=%s)", rec.Code, rec.Body.String())
		}
		st := e.readContact(t, id)
		if st.guestPhone == nil || *st.guestPhone != "+962700000001" {
			t.Fatalf("guest_phone changed on 422: %v want +962700000001", st.guestPhone)
		}
	})

	// ── T11: COALESCE proof — name-only leaves phone untouched, phone-only leaves name untouched ──
	t.Run("coalesce_partial_updates", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		e.setGuestPhone(t, id, "+962711111111")

		if rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", id), gin.H{"guest_name": "خالد"}); rec.Code != 200 {
			t.Fatalf("name-only status=%d body=%s", rec.Code, rec.Body.String())
		}
		st := e.readContact(t, id)
		if st.guestPhone == nil || *st.guestPhone != "+962711111111" {
			t.Fatalf("phone touched by name-only update: %v", st.guestPhone)
		}
		if st.guestName == nil || *st.guestName != "خالد" {
			t.Fatalf("name not updated: %v", st.guestName)
		}

		if rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", id), gin.H{"guest_phone": "0799999999"}); rec.Code != 200 {
			t.Fatalf("phone-only status=%d body=%s", rec.Code, rec.Body.String())
		}
		st2 := e.readContact(t, id)
		if st2.guestName == nil || *st2.guestName != "خالد" {
			t.Fatalf("name touched by phone-only update: %v", st2.guestName)
		}
		if st2.guestPhone == nil || *st2.guestPhone != "+962799999999" {
			t.Fatalf("phone not updated: %v", st2.guestPhone)
		}
	})

	// ── T13: GET as staff → 403 ──
	t.Run("get_staff_403", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchStaff, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		rec := bsDo(staffR, http.MethodGet, fmt.Sprintf("/bookings/%d/contact", id), nil)
		if rec.Code != 403 {
			t.Fatalf("status=%d want 403 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── T14: GET cross-tenant → 404, no phone substring in body ──
	t.Run("get_cross_tenant_404_no_phone", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchB, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		e.setGuestPhone(t, id, "+962722222222")
		rec := bsDo(ownerA, http.MethodGet, fmt.Sprintf("/bookings/%d/contact", id), nil)
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "722222222") {
			t.Fatalf("phone leaked in 404 body: %s", rec.Body.String())
		}
	})

	// ── T15: GET on source='player' → 404, contact_phone never appears in body ──
	t.Run("get_player_source_404_no_contact_phone", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 25, nil, "unpaid")
		if _, err := e.pool.Exec(context.Background(),
			`UPDATE bookings SET contact_name='Player Snap', contact_phone='+962733333333' WHERE id=$1`, id); err != nil {
			t.Fatalf("seed contact snapshot: %v", err)
		}
		rec := bsDo(ownerA, http.MethodGet, fmt.Sprintf("/bookings/%d/contact", id), nil)
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "733333333") || strings.Contains(rec.Body.String(), "Player Snap") {
			t.Fatalf("contact snapshot leaked via /contact endpoint: %s", rec.Body.String())
		}
	})

	// ── T16: GET on source='block' → 404 ──
	t.Run("get_block_404", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "block", "confirmed", nil, s, en, 0, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodGet, fmt.Sprintf("/bookings/%d/contact", id), nil)
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── T17: GET on cancelled booking → 404 ──
	t.Run("get_cancelled_404", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "cancelled", nil, s, en, 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodGet, fmt.Sprintf("/bookings/%d/contact", id), nil)
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── GET happy path: recurrence_group_id round-trips (feeds the frontend G2 hint) ──
	t.Run("get_ok_returns_fields", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "academy", "confirmed", nil, s, en, 25, nil, "unpaid")
		e.setGuestPhone(t, id, "+962744444444")
		rec := bsDo(ownerA, http.MethodGet, fmt.Sprintf("/bookings/%d/contact", id), nil)
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		data := decodeContact(t, rec.Body.Bytes())
		if data["guest_phone"] != "+962744444444" {
			t.Fatalf("guest_phone=%v want +962744444444", data["guest_phone"])
		}
		if data["recurrence_group_id"] != nil {
			t.Fatalf("recurrence_group_id=%v want nil (non-series booking)", data["recurrence_group_id"])
		}
	})

	// ── T18: regression guard — the schedule day-view list contains NO phone value ──
	t.Run("schedule_list_never_leaks_phone", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		e.setGuestPhone(t, id, "+962755555555")
		dateStr := s.In(timeutil.Amman()).Format("2006-01-02")
		rec := bsDo(ownerA, http.MethodGet, fmt.Sprintf("/schedule?date=%s&pitch_id=%d", dateStr, e.pitchA), nil)
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "755555555") || strings.Contains(rec.Body.String(), "guest_phone") {
			t.Fatalf("schedule list leaked a phone value — the list query must never carry guest_phone: %s", rec.Body.String())
		}
	})
}
