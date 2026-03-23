// Package cron provides an RRule-based scheduler for recurring automations.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule represents a parsed RRule recurrence schedule.
type Schedule struct {
	freq     string // DAILY, WEEKLY, MONTHLY, YEARLY
	interval int
	byDay    []time.Weekday // for WEEKLY
}

// Parse parses a simplified RRule string (FREQ=...[;INTERVAL=...][;BYDAY=...]).
func Parse(rrule string) (Schedule, error) {
	if rrule == "" {
		return Schedule{}, fmt.Errorf("rrule: empty expression")
	}
	s := Schedule{interval: 1}
	for _, part := range strings.Split(rrule, ";") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key, val := kv[0], kv[1]
		switch key {
		case "FREQ":
			switch val {
			case "DAILY", "WEEKLY", "MONTHLY", "YEARLY":
				s.freq = val
			default:
				return Schedule{}, fmt.Errorf("rrule: unknown FREQ %q", val)
			}
		case "INTERVAL":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return Schedule{}, fmt.Errorf("rrule: invalid INTERVAL %q", val)
			}
			s.interval = n
		case "BYDAY":
			dayMap := map[string]time.Weekday{
				"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday,
				"WE": time.Wednesday, "TH": time.Thursday, "FR": time.Friday,
				"SA": time.Saturday,
			}
			for _, d := range strings.Split(val, ",") {
				wd, ok := dayMap[d]
				if !ok {
					return Schedule{}, fmt.Errorf("rrule: unknown day %q", d)
				}
				s.byDay = append(s.byDay, wd)
			}
		}
	}
	if s.freq == "" {
		return Schedule{}, fmt.Errorf("rrule: missing FREQ")
	}
	return s, nil
}

// Next returns the next occurrence after t.
func (s Schedule) Next(t time.Time) time.Time {
	t = t.Add(time.Minute).Truncate(time.Minute)

	switch s.freq {
	case "DAILY":
		return t.AddDate(0, 0, s.interval).Truncate(24 * time.Hour).Add(t.Sub(t.Truncate(24 * time.Hour)))
	case "WEEKLY":
		if len(s.byDay) == 0 {
			return t.AddDate(0, 0, 7*s.interval)
		}
		// Find the next matching weekday within the interval window.
		for i := 1; i <= 7*s.interval; i++ {
			candidate := t.AddDate(0, 0, i)
			for _, wd := range s.byDay {
				if candidate.Weekday() == wd {
					return candidate
				}
			}
		}
		return t.AddDate(0, 0, 7*s.interval)
	case "MONTHLY":
		return t.AddDate(0, s.interval, 0)
	case "YEARLY":
		return t.AddDate(s.interval, 0, 0)
	}
	return time.Time{}
}
