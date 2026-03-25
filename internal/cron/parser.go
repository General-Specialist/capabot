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
	byHour   int            // -1 = not set
	byMinute int            // -1 = not set
}

// Parse parses a simplified RRule string (FREQ=...[;INTERVAL=...][;BYDAY=...]).
func Parse(rrule string) (Schedule, error) {
	if rrule == "" {
		return Schedule{}, fmt.Errorf("rrule: empty expression")
	}
	s := Schedule{interval: 1, byHour: -1, byMinute: -1}
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
		case "BYHOUR":
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 || n > 23 {
				return Schedule{}, fmt.Errorf("rrule: invalid BYHOUR %q", val)
			}
			s.byHour = n
		case "BYMINUTE":
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 || n > 59 {
				return Schedule{}, fmt.Errorf("rrule: invalid BYMINUTE %q", val)
			}
			s.byMinute = n
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

	var next time.Time
	switch s.freq {
	case "DAILY":
		next = t.AddDate(0, 0, s.interval).Truncate(24 * time.Hour).Add(t.Sub(t.Truncate(24 * time.Hour)))
	case "WEEKLY":
		if len(s.byDay) == 0 {
			next = t.AddDate(0, 0, 7*s.interval)
		} else {
			// Find the next matching weekday within the interval window.
			next = t.AddDate(0, 0, 7*s.interval)
			for i := 1; i <= 7*s.interval; i++ {
				candidate := t.AddDate(0, 0, i)
				for _, wd := range s.byDay {
					if candidate.Weekday() == wd {
						next = candidate
						goto foundWeekday
					}
				}
			}
		foundWeekday:
		}
	case "MONTHLY":
		next = t.AddDate(0, s.interval, 0)
	case "YEARLY":
		next = t.AddDate(s.interval, 0, 0)
	default:
		return time.Time{}
	}

	// Apply BYHOUR / BYMINUTE if specified.
	if s.byHour >= 0 || s.byMinute >= 0 {
		h := next.Hour()
		m := next.Minute()
		if s.byHour >= 0 {
			h = s.byHour
		}
		if s.byMinute >= 0 {
			m = s.byMinute
		}
		next = time.Date(next.Year(), next.Month(), next.Day(), h, m, 0, 0, next.Location())
	}
	return next
}
