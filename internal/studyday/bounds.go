// Package studyday calculates user-local study-day boundaries.
package studyday

import "time"

// Bounds returns the current user's study-day interval in UTC. A nonexistent
// local start time resolves to the first valid wall time after the gap; an
// ambiguous start time resolves to its earliest UTC occurrence.
func Bounds(now time.Time, location *time.Location, startHour int) (time.Time, time.Time, bool) {
	if location == nil || startHour < 0 || startHour > 23 {
		return time.Time{}, time.Time{}, false
	}

	localNow := now.In(location)
	year, month, day := localNow.Date()
	date := calendarDate(year, month, day)
	start := Boundary(date, startHour, location)
	if localNow.Before(start) {
		date = addCalendarDays(date, -1)
		start = Boundary(date, startHour, location)
	}
	end := Boundary(addCalendarDays(date, 1), startHour, location)
	return start, end, true
}

// Boundary resolves the configured study-day start for a local calendar date.
// It handles daylight-saving gaps and folds deterministically instead of
// relying on time.Date's implementation-dependent normalization.
func Boundary(date time.Time, startHour int, location *time.Location) time.Time {
	year, month, day := date.UTC().Date()
	targetWall := time.Date(year, month, day, startHour, 0, 0, 0, time.UTC)
	if location == nil || startHour < 0 || startHour > 23 {
		return targetWall
	}

	const scanStep = 15 * time.Minute
	scanStart := targetWall.Add(-48 * time.Hour)
	scanEnd := targetWall.Add(48 * time.Hour)
	offsets := make(map[int]struct{})
	transitions := make([]zoneTransition, 0, 2)
	previous := scanStart
	_, previousOffset := previous.In(location).Zone()
	offsets[previousOffset] = struct{}{}
	for sample := scanStart.Add(scanStep); !sample.After(scanEnd); sample = sample.Add(scanStep) {
		_, offset := sample.In(location).Zone()
		offsets[offset] = struct{}{}
		if offset != previousOffset {
			at := findOffsetTransition(previous, sample, previousOffset, location)
			_, before := at.Add(-time.Second).In(location).Zone()
			_, after := at.In(location).Zone()
			transitions = append(transitions, zoneTransition{at: at, before: before, after: after})
			offsets[before] = struct{}{}
			offsets[after] = struct{}{}
		}
		previous, previousOffset = sample, offset
	}

	var earliest time.Time
	for offset := range offsets {
		candidate := targetWall.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(location)
		if sameLocalDate(local, year, month, day) && local.Hour() == startHour && local.Minute() == 0 && local.Second() == 0 {
			if earliest.IsZero() || candidate.Before(earliest) {
				earliest = candidate
			}
		}
	}
	if !earliest.IsZero() {
		return earliest.UTC()
	}

	for _, transition := range transitions {
		if transition.after <= transition.before {
			continue
		}
		gapStart := transition.at.Add(time.Duration(transition.before) * time.Second)
		gapEnd := transition.at.Add(time.Duration(transition.after) * time.Second)
		if !targetWall.Before(gapStart) && targetWall.Before(gapEnd) {
			return transition.at.UTC()
		}
	}

	// Keep an explicit deterministic fallback for unusual historical zone data.
	var bestInstant, bestWall time.Time
	for offset := range offsets {
		candidate := targetWall.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(location)
		if !sameLocalDate(local, year, month, day) {
			continue
		}
		wall := time.Date(year, month, day, local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.UTC)
		if wall.Before(targetWall) {
			continue
		}
		if bestWall.IsZero() || wall.Before(bestWall) || (wall.Equal(bestWall) && candidate.Before(bestInstant)) {
			bestInstant, bestWall = candidate, wall
		}
	}
	if !bestInstant.IsZero() {
		return bestInstant.UTC()
	}
	return time.Date(year, month, day, startHour, 0, 0, 0, location).UTC()
}

type zoneTransition struct {
	at     time.Time
	before int
	after  int
}

func findOffsetTransition(low, high time.Time, oldOffset int, location *time.Location) time.Time {
	lowUnix, highUnix := low.Unix(), high.Unix()
	for highUnix-lowUnix > 1 {
		middle := lowUnix + (highUnix-lowUnix)/2
		_, offset := time.Unix(middle, 0).In(location).Zone()
		if offset == oldOffset {
			lowUnix = middle
		} else {
			highUnix = middle
		}
	}
	return time.Unix(highUnix, 0).UTC()
}

func sameLocalDate(local time.Time, year int, month time.Month, day int) bool {
	localYear, localMonth, localDay := local.Date()
	return localYear == year && localMonth == month && localDay == day
}

func calendarDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 12, 0, 0, 0, time.UTC)
}

func addCalendarDays(date time.Time, days int) time.Time {
	year, month, day := date.UTC().Date()
	return calendarDate(year, month, day+days)
}
