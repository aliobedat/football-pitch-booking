package data

import (
	"errors"
	"testing"
)

// Weekday constants for readable test windows (0=Sun … 6=Sat).
const (
	sun = 0
	mon = 1
	tue = 2
	wed = 3
	thu = 4
	fri = 5
	sat = 6
)

func win(weekday int, open, close string) OperatingWindow {
	return OperatingWindow{Weekday: weekday, OpenTime: open, CloseTime: close}
}

func TestValidateSchedule(t *testing.T) {
	tests := []struct {
		name    string
		windows []OperatingWindow
		wantErr bool
	}{
		{
			name:    "empty schedule is valid (open 24/7 fail-open)",
			windows: nil,
			wantErr: false,
		},
		{
			name:    "single normal window",
			windows: []OperatingWindow{win(mon, "09:00", "17:00")},
			wantErr: false,
		},
		{
			name: "adjacent windows do NOT overlap (touching ends are legal)",
			windows: []OperatingWindow{
				win(mon, "16:00", "18:00"),
				win(mon, "18:00", "20:00"),
			},
			wantErr: false,
		},
		{
			name: "intra-day overlap is rejected",
			windows: []OperatingWindow{
				win(mon, "09:00", "12:00"),
				win(mon, "11:00", "14:00"),
			},
			wantErr: true,
		},
		{
			name: "identical windows overlap",
			windows: []OperatingWindow{
				win(tue, "10:00", "12:00"),
				win(tue, "10:00", "12:00"),
			},
			wantErr: true,
		},
		{
			name:    "open == close is rejected (ambiguous zero-length window)",
			windows: []OperatingWindow{win(wed, "10:00", "10:00")},
			wantErr: true,
		},
		{
			name:    "lone cross-midnight window is valid",
			windows: []OperatingWindow{win(thu, "16:00", "02:00")},
			wantErr: false,
		},
		{
			name: "cross-midnight spill overlaps NEXT-day window (Thu 16→02 vs Fri 01→05)",
			windows: []OperatingWindow{
				win(thu, "16:00", "02:00"),
				win(fri, "01:00", "05:00"),
			},
			wantErr: true,
		},
		{
			name: "cross-midnight spill is adjacent to next-day window (Thu 16→02 then Fri 02→05 is legal)",
			windows: []OperatingWindow{
				win(thu, "16:00", "02:00"),
				win(fri, "02:00", "05:00"),
			},
			wantErr: false,
		},
		{
			name: "cross-midnight tail does NOT touch a later same-prev-day window (Thu 16→02 + Thu 10→14 legal)",
			windows: []OperatingWindow{
				win(thu, "16:00", "02:00"),
				win(thu, "10:00", "14:00"),
			},
			wantErr: false,
		},
		{
			name: "Saturday→Sunday wrap: Sat 22→03 overlaps Sun 01→05",
			windows: []OperatingWindow{
				win(sat, "22:00", "03:00"),
				win(sun, "01:00", "05:00"),
			},
			wantErr: true,
		},
		{
			name: "Saturday→Sunday wrap: Sat 22→03 adjacent to Sun 03→06 is legal",
			windows: []OperatingWindow{
				win(sat, "22:00", "03:00"),
				win(sun, "03:00", "06:00"),
			},
			wantErr: false,
		},
		{
			name:    "weekday out of range is rejected",
			windows: []OperatingWindow{win(7, "09:00", "10:00")},
			wantErr: true,
		},
		{
			name:    "malformed time is rejected",
			windows: []OperatingWindow{win(mon, "9am", "17:00")},
			wantErr: true,
		},
		{
			name: "many non-overlapping windows across the week",
			windows: []OperatingWindow{
				win(sun, "08:00", "12:00"),
				win(sun, "16:00", "22:00"),
				win(mon, "09:00", "17:00"),
				win(sat, "20:00", "01:00"), // crosses into Sunday but before Sun 08:00 window
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSchedule(tt.windows)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tt.wantErr && err != nil && !errors.Is(err, ErrInvalidOperatingHours) {
				t.Fatalf("error %v does not wrap ErrInvalidOperatingHours", err)
			}
		})
	}
}

func TestCrossesMidnight(t *testing.T) {
	tests := []struct {
		name string
		w    OperatingWindow
		want bool
	}{
		{"normal window", win(mon, "09:00", "17:00"), false},
		{"cross-midnight window", win(thu, "16:00", "02:00"), true},
		{"close just before midnight", win(mon, "16:00", "23:59"), false},
		{"close at 00:00 spills (00:00 <= open)", win(mon, "16:00", "00:00"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.w.CrossesMidnight()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("CrossesMidnight() = %v, want %v", got, tt.want)
			}
		})
	}
}
