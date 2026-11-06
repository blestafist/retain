package clock

import (
	"testing"
	"time"
)

var _ Clock = Real{}

type fakeClock struct {
	now time.Time
}

func (c fakeClock) Now() time.Time {
	return c.now
}

var _ Clock = fakeClock{}

func TestRealNowReturnsUTC(t *testing.T) {
	got := (Real{}).Now()
	if got.Location() != time.UTC {
		t.Errorf("Now() location = %v, want UTC", got.Location())
	}
}

func TestFakeClockReturnsConfiguredTime(t *testing.T) {
	want := time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC)
	var c Clock = fakeClock{now: want}

	if got := c.Now(); !got.Equal(want) {
		t.Errorf("Now() = %v, want %v", got, want)
	}
}
