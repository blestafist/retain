package studyday

import (
	"testing"
	"time"
)

func TestBoundsResolvesDSTGapsAndFoldsConsistently(t *testing.T) {
	tests := []struct {
		name      string
		timezone  string
		now       time.Time
		startHour int
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "spring gap starts at first valid local time",
			timezone:  "America/Los_Angeles",
			now:       time.Date(2026, time.March, 8, 10, 30, 0, 0, time.UTC),
			startHour: 2,
			wantStart: time.Date(2026, time.March, 8, 10, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, time.March, 9, 9, 0, 0, 0, time.UTC),
		},
		{
			name:      "fall fold uses earliest boundary",
			timezone:  "Europe/Berlin",
			now:       time.Date(2025, time.October, 26, 1, 30, 0, 0, time.UTC),
			startHour: 2,
			wantStart: time.Date(2025, time.October, 26, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2025, time.October, 27, 1, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			location, err := time.LoadLocation(tt.timezone)
			if err != nil {
				t.Fatal(err)
			}
			start, end, ok := Bounds(tt.now, location, tt.startHour)
			if !ok {
				t.Fatal("Bounds() rejected valid input")
			}
			if !start.Equal(tt.wantStart) || !end.Equal(tt.wantEnd) {
				t.Errorf("Bounds() = [%s,%s), want [%s,%s)", start, end, tt.wantStart, tt.wantEnd)
			}
			if !(start.Before(end)) {
				t.Errorf("Bounds() returned non-increasing boundaries: [%s,%s)", start, end)
			}
		})
	}
}

func TestBoundsRejectsInvalidArguments(t *testing.T) {
	for _, tt := range []struct {
		name      string
		location  *time.Location
		startHour int
	}{
		{name: "nil location", startHour: 4},
		{name: "negative hour", location: time.UTC, startHour: -1},
		{name: "hour after day", location: time.UTC, startHour: 24},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, ok := Bounds(time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC), tt.location, tt.startHour); ok {
				t.Fatal("Bounds() accepted invalid arguments")
			}
		})
	}
}
