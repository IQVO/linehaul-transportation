package network

import (
	"strings"
	"time"
)

var weekdayNames = [...]string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

// WeekdayName returns the lowercase English name of d, the key node-topology
// uses in operatingHoursByDay and in cutoff days.
func WeekdayName(d time.Weekday) string { return weekdayNames[d] }

// ParseWeekday parses a weekday name, case-insensitively (monday, MONDAY).
func ParseWeekday(s string) (time.Weekday, bool) {
	for i, n := range weekdayNames {
		if strings.EqualFold(s, n) {
			return time.Weekday(i), true
		}
	}
	return 0, false
}

// ParseClock parses a strict "HH:mm" local time of day into minutes since
// midnight (0 to 1439).
func ParseClock(s string) (int, bool) {
	if len(s) != 5 {
		return 0, false
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}
