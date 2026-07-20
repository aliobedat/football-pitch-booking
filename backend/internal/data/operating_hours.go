package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ali/football-pitch-api/internal/auth"
)

// ─────────────────────────────────────────────────────────────────────────────
// Operating hours — per-pitch weekly open-window schedule
//
// One OperatingWindow = one open window on one weekday, expressed in Asia/Amman
// wall-clock (TIME, no timezone) because the schedule recurs weekly. The absolute
// instant is resolved at query time (see ResolveOpenWindows, Phase 2), never
// stored. Cross-midnight is DERIVED: a window whose close_time <= open_time spills
// into the next weekday (e.g. Thu 16:00 → 02:00 covers Thu 16:00 → Fri 02:00).
//
// Weekday convention (pinned): 0 = Sunday … 6 = Saturday — matches Postgres
// EXTRACT(DOW) and Go time.Weekday. Do not deviate.
// ─────────────────────────────────────────────────────────────────────────────

// weekMinutes is the number of minutes in a 7-day week. Overlap detection maps
// each window onto this circular minute line so cross-midnight spill and the
// Saturday→Sunday wrap are handled uniformly.
const weekMinutes = 7 * 24 * 60

// ErrInvalidOperatingHours is returned by ValidateSchedule when the submitted
// schedule is malformed (bad weekday/time, open==close, or an overlap including
// cross-midnight spillover). The handler maps it to 400; the wrapped message is
// safe to surface to the client.
var ErrInvalidOperatingHours = errors.New("operating hours: invalid schedule")

// OperatingWindow is one open window on one weekday. OpenTime / CloseTime are
// "HH:MM" 24-hour Asia/Amman wall-clock strings (the wire + storage format).
type OperatingWindow struct {
	Weekday   int    `json:"weekday"`    // 0=Sun … 6=Sat
	OpenTime  string `json:"open_time"`  // "HH:MM"
	CloseTime string `json:"close_time"` // "HH:MM"
}

// CrossesMidnight reports whether this window spills past midnight into the next
// weekday. close <= open means it does (close == open is rejected by validation
// except for the sole-window 24-hour case, which is NOT a cross-midnight spill —
// it is a single self-contained full civil day. Callers that need to distinguish
// the two should check IsFullDay first).
func (w OperatingWindow) CrossesMidnight() (bool, error) {
	o, err := parseHHMM(w.OpenTime)
	if err != nil {
		return false, err
	}
	c, err := parseHHMM(w.CloseTime)
	if err != nil {
		return false, err
	}
	if o == c {
		return false, nil // full-day window (00:00->00:00), not a spill
	}
	return c <= o, nil
}

// IsFullDay reports whether this window is the explicit 24-hour representation:
// open_time == close_time == "00:00". This is the ONLY equal-time pair
// ValidateSchedule accepts, and only when it is the sole window for its weekday.
func (w OperatingWindow) IsFullDay() (bool, error) {
	o, err := parseHHMM(w.OpenTime)
	if err != nil {
		return false, err
	}
	c, err := parseHHMM(w.CloseTime)
	if err != nil {
		return false, err
	}
	return o == 0 && c == 0, nil
}

// parseHHMM parses an "HH:MM" 24-hour string into minutes-since-midnight [0,1440).
func parseHHMM(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("%w: time %q must be HH:MM (24-hour)", ErrInvalidOperatingHours, s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// weekInterval is a window projected onto the circular week-minute line, used
// only for overlap detection. [start, end) is half-open so adjacent windows
// (16:00–18:00 then 18:00–20:00) do NOT overlap. end may exceed weekMinutes when
// the window crosses midnight (it is unwrapped against ±weekMinutes when compared).
type weekInterval struct {
	start int
	end   int
}

// toWeekInterval projects a window onto the circular week-minute line. A
// non-crossing window stays within its weekday; a cross-midnight window's end
// extends 1440 minutes past its weekday start (spilling into the next day, and
// past weekMinutes for a Saturday window — the wrap is handled at compare time).
func (w OperatingWindow) toWeekInterval() (weekInterval, error) {
	o, err := parseHHMM(w.OpenTime)
	if err != nil {
		return weekInterval{}, err
	}
	c, err := parseHHMM(w.CloseTime)
	if err != nil {
		return weekInterval{}, err
	}
	start := w.Weekday*24*60 + o
	if o == c { // full-day (00:00->00:00): exactly 1440 minutes, no more/less
		return weekInterval{start: start, end: start + 24*60}, nil
	}
	end := w.Weekday*24*60 + c
	if c < o { // cross-midnight: close is on the following day
		end += 24 * 60
	}
	return weekInterval{start: start, end: end}, nil
}

// overlaps reports whether two week intervals intersect on the CIRCULAR week
// line. Each window is < 1440 min long with start in [0, weekMinutes), so testing
// b at three rotations (−1 week, 0, +1 week) covers the Saturday→Sunday wrap: a
// Saturday cross-midnight window (end > weekMinutes) is compared against early
// Sunday windows via the −weekMinutes shift. Half-open [start,end): touching ends
// (a.end == b.start) are adjacent, not overlapping.
func (a weekInterval) overlaps(b weekInterval) bool {
	for _, shift := range [...]int{-weekMinutes, 0, weekMinutes} {
		bs, be := b.start+shift, b.end+shift
		if a.start < be && bs < a.end {
			return true
		}
	}
	return false
}

// ValidateSchedule fail-closes on a malformed weekly schedule. It rejects:
//   - a weekday outside 0..6,
//   - a time not parseable as HH:MM,
//   - open_time == close_time, UNLESS it is "00:00"/"00:00" (the explicit
//     24-hour-open representation) AND it is the ONLY window for that weekday.
//     A non-midnight equal-time pair, or a 00:00->00:00 window combined with any
//     other window on the same weekday, is still rejected.
//   - any two windows that overlap — INCLUDING cross-midnight spillover into the
//     next day and the Saturday→Sunday week wrap (e.g. Thu 16:00→02:00 overlaps
//     Fri 01:00→05:00). Adjacent windows (…→18:00 then 18:00→…) are legal.
//
// An empty schedule is valid: zero windows means the pitch is OPEN 24/7
// (fail-open on unconfigured data — see the PR's Phase 0 decision). A weekday
// with zero windows (while OTHER weekdays have configured windows) means that
// day is CLOSED — this is a distinct state from the 24-hour representation.
func ValidateSchedule(windows []OperatingWindow) error {
	perWeekday := make(map[int]int, 7)
	for _, w := range windows {
		if w.Weekday >= 0 && w.Weekday <= 6 {
			perWeekday[w.Weekday]++
		}
	}

	intervals := make([]weekInterval, 0, len(windows))
	for i, w := range windows {
		if w.Weekday < 0 || w.Weekday > 6 {
			return fmt.Errorf("%w: weekday %d out of range (0=Sun..6=Sat)", ErrInvalidOperatingHours, w.Weekday)
		}
		o, err := parseHHMM(w.OpenTime)
		if err != nil {
			return err
		}
		c, err := parseHHMM(w.CloseTime)
		if err != nil {
			return err
		}
		if o == c {
			if o != 0 {
				return fmt.Errorf("%w: window %d on weekday %d has equal open and close time (%s)",
					ErrInvalidOperatingHours, i, w.Weekday, w.OpenTime)
			}
			if perWeekday[w.Weekday] != 1 {
				return fmt.Errorf("%w: window %d on weekday %d is a 24-hour window (00:00->00:00) but is not the sole window for that day",
					ErrInvalidOperatingHours, i, w.Weekday)
			}
		}
		iv, err := w.toWeekInterval()
		if err != nil {
			return err
		}
		intervals = append(intervals, iv)
	}

	// Pairwise overlap on the circular week line. The window count per pitch is
	// small (≤ a few dozen), so O(n²) is fine and keeps the logic obvious.
	for i := 0; i < len(intervals); i++ {
		for j := i + 1; j < len(intervals); j++ {
			if intervals[i].overlaps(intervals[j]) {
				return fmt.Errorf("%w: windows %d and %d overlap (check cross-midnight spillover)",
					ErrInvalidOperatingHours, i, j)
			}
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Persistence
// ─────────────────────────────────────────────────────────────────────────────

// GetOperatingHours returns the weekly schedule for a pitch, ordered by weekday
// then open_time. It is readable for any live (non-deleted) pitch — players need
// it to render bookable/closed, owners to seed the editor. A missing or
// soft-deleted pitch yields pgx.ErrNoRows (→ 404). An empty slice means the pitch
// has no configured hours (treated as open 24/7 by the resolver).
func (m *PitchModel) GetOperatingHours(ctx context.Context, pitchID int) ([]OperatingWindow, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Confirm the pitch exists and is live, so a non-existent id is a 404 rather
	// than an empty "open 24/7" schedule that would mislead the caller.
	var exists bool
	if err := m.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pitches WHERE id = $1 AND deleted_at IS NULL)`,
		pitchID,
	).Scan(&exists); err != nil {
		return nil, fmt.Errorf("GetOperatingHours: pitch lookup: %w", err)
	}
	if !exists {
		return nil, pgx.ErrNoRows
	}

	rows, err := m.DB.Query(ctx, `
		SELECT weekday, to_char(open_time, 'HH24:MI'), to_char(close_time, 'HH24:MI')
		FROM operating_hours
		WHERE pitch_id = $1
		ORDER BY weekday, open_time
	`, pitchID)
	if err != nil {
		return nil, fmt.Errorf("GetOperatingHours: query: %w", err)
	}
	defer rows.Close()

	windows := []OperatingWindow{}
	for rows.Next() {
		var w OperatingWindow
		if err := rows.Scan(&w.Weekday, &w.OpenTime, &w.CloseTime); err != nil {
			return nil, fmt.Errorf("GetOperatingHours: scan: %w", err)
		}
		windows = append(windows, w)
	}
	return windows, rows.Err()
}

// ReplaceOperatingHours wholesale-replaces a pitch's weekly schedule with the
// given window set, scoped to the actor, in ONE transaction:
//
//  1. Resolve + LOCK the pitch with the ownership predicate (owner → only their
//     own; admin → any; live pitches only). A missing/foreign/soft-deleted row
//     yields pgx.ErrNoRows → 404, never leaking another owner's resource. This
//     reuses the same resolve-and-lock pattern as SetPitchActive / SoftDeletePitch.
//  2. DELETE all existing windows for the pitch, then INSERT the new set. Hard
//     replace (no soft-delete): operating hours are wholesale-replaced config, so
//     the audited unit is the change event, not the row.
//  3. Write a pitch_audit_log row (actor, 'operating_hours_updated') whose JSONB
//     `detail` carries the post-change window snapshot.
//
// The caller MUST have validated `windows` (ValidateSchedule) first — this method
// does not re-validate; it owns persistence + audit, not the business rule.
func (m *PitchModel) ReplaceOperatingHours(ctx context.Context, pitchID int, actor auth.Actor, windows []OperatingWindow) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := m.DB.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ReplaceOperatingHours: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 1. Resolve + lock with the ownership predicate.
	var resolvedID int
	if actor.IsAdmin() {
		err = tx.QueryRow(ctx,
			`SELECT id FROM pitches WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
			pitchID,
		).Scan(&resolvedID)
	} else {
		err = tx.QueryRow(ctx,
			`SELECT id FROM pitches WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL FOR UPDATE`,
			pitchID, actor.UserID,
		).Scan(&resolvedID)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgx.ErrNoRows // → 404 (not found OR not owned)
		}
		return fmt.Errorf("ReplaceOperatingHours: resolve: %w", err)
	}

	// 2. Hard replace: clear then re-insert the full set.
	if _, err = tx.Exec(ctx, `DELETE FROM operating_hours WHERE pitch_id = $1`, pitchID); err != nil {
		return fmt.Errorf("ReplaceOperatingHours: delete: %w", err)
	}
	for _, w := range windows {
		if _, err = tx.Exec(ctx, `
			INSERT INTO operating_hours (pitch_id, weekday, open_time, close_time)
			VALUES ($1, $2, $3::time, $4::time)
		`, pitchID, w.Weekday, w.OpenTime, w.CloseTime); err != nil {
			return fmt.Errorf("ReplaceOperatingHours: insert: %w", err)
		}
	}

	// 3. Audit the change event with the post-change snapshot. A stable
	//    weekday/open ordering makes the snapshot diff-friendly across versions.
	snapshot := append([]OperatingWindow(nil), windows...)
	sort.Slice(snapshot, func(i, j int) bool {
		if snapshot[i].Weekday != snapshot[j].Weekday {
			return snapshot[i].Weekday < snapshot[j].Weekday
		}
		return snapshot[i].OpenTime < snapshot[j].OpenTime
	})
	detail, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("ReplaceOperatingHours: marshal snapshot: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO pitch_audit_log (pitch_id, actor_id, actor_role, action, detail)
		VALUES ($1, $2, $3, 'operating_hours_updated', $4)
	`, pitchID, actor.UserID, actor.Role, detail); err != nil {
		return fmt.Errorf("ReplaceOperatingHours: audit: %w", err)
	}

	return tx.Commit(ctx)
}
