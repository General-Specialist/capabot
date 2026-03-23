// Package cron provides a minimal 5-field cron expression parser and scheduler.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule represents a parsed cron expression.
type Schedule struct {
	min     field
	hour    field
	dom     field // day of month
	month   field
	dow     field // day of week (0=Sunday)
}

type field struct {
	bits uint64 // bit i set means value i is active
	star bool   // true = wildcard (match any)
}

// Parse parses a standard 5-field cron expression (min hour dom month dow).
func Parse(expr string) (Schedule, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("cron: expected 5 fields, got %d", len(parts))
	}
	var s Schedule
	var err error
	if s.min, err = parseField(parts[0], 0, 59); err != nil {
		return Schedule{}, fmt.Errorf("cron minute: %w", err)
	}
	if s.hour, err = parseField(parts[1], 0, 23); err != nil {
		return Schedule{}, fmt.Errorf("cron hour: %w", err)
	}
	if s.dom, err = parseField(parts[2], 1, 31); err != nil {
		return Schedule{}, fmt.Errorf("cron dom: %w", err)
	}
	if s.month, err = parseField(parts[3], 1, 12); err != nil {
		return Schedule{}, fmt.Errorf("cron month: %w", err)
	}
	if s.dow, err = parseField(parts[4], 0, 6); err != nil {
		return Schedule{}, fmt.Errorf("cron dow: %w", err)
	}
	return s, nil
}

// Next returns the next time after t that matches the schedule.
func (s Schedule) Next(t time.Time) time.Time {
	// Advance by one minute to avoid returning t itself.
	t = t.Add(time.Minute).Truncate(time.Minute)

	// Search up to ~4 years to handle edge cases.
	for i := 0; i < 366*4*24*60; i++ {
		if !s.month.matches(int(t.Month())) {
			t = nextMonth(t)
			continue
		}
		if !s.dom.matches(t.Day()) || !s.dow.matches(int(t.Weekday())) {
			t = nextDay(t)
			continue
		}
		if !s.hour.matches(t.Hour()) {
			t = nextHour(t)
			continue
		}
		if !s.min.matches(t.Minute()) {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{} // unreachable for valid expressions
}

func (f field) matches(v int) bool {
	if f.star {
		return true
	}
	return f.bits&(1<<uint(v)) != 0
}

func parseField(s string, min, max int) (field, error) {
	if s == "*" {
		return field{star: true}, nil
	}
	var f field
	for _, part := range strings.Split(s, ",") {
		if err := applyPart(&f, part, min, max); err != nil {
			return field{}, err
		}
	}
	return f, nil
}

func applyPart(f *field, part string, min, max int) error {
	// Handle step: */n or range/n
	step := 1
	if idx := strings.Index(part, "/"); idx >= 0 {
		var err error
		step, err = strconv.Atoi(part[idx+1:])
		if err != nil || step < 1 {
			return fmt.Errorf("invalid step %q", part[idx+1:])
		}
		part = part[:idx]
	}

	// Determine range
	lo, hi := min, max
	if part != "*" {
		if idx := strings.Index(part, "-"); idx >= 0 {
			var err error
			lo, err = strconv.Atoi(part[:idx])
			if err != nil {
				return fmt.Errorf("invalid range start %q", part[:idx])
			}
			hi, err = strconv.Atoi(part[idx+1:])
			if err != nil {
				return fmt.Errorf("invalid range end %q", part[idx+1:])
			}
		} else {
			v, err := strconv.Atoi(part)
			if err != nil {
				return fmt.Errorf("invalid value %q", part)
			}
			lo, hi = v, v
		}
	}

	if lo < min || hi > max || lo > hi {
		return fmt.Errorf("value %d-%d out of range %d-%d", lo, hi, min, max)
	}

	for v := lo; v <= hi; v += step {
		f.bits |= 1 << uint(v)
	}
	return nil
}

func nextMonth(t time.Time) time.Time {
	// Jump to first day of next month
	y, m, _ := t.Date()
	if m == 12 {
		return time.Date(y+1, 1, 1, 0, 0, 0, 0, t.Location())
	}
	return time.Date(y, m+1, 1, 0, 0, 0, 0, t.Location())
}

func nextDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, t.Location())
}

func nextHour(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, t.Hour()+1, 0, 0, 0, t.Location())
}
