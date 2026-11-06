package clock

import "time"

// Clock provides the current time.
type Clock interface {
	Now() time.Time
}

// Real reads the current system time in UTC.
type Real struct{}

func (Real) Now() time.Time {
	return time.Now().UTC()
}
