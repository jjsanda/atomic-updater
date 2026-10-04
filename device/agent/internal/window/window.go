// Package window evaluates maintenance windows ("HH:MM-HH:MM", local time).
package window

import (
	"fmt"
	"strings"
	"time"
)

// Window is a daily time-of-day interval. Zero value = always open.
// Windows may cross midnight ("22:00-02:00").
type Window struct {
	start, end time.Duration // offsets from local midnight
	always     bool
}

func Parse(spec string) (Window, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Window{always: true}, nil
	}
	from, to, found := strings.Cut(spec, "-")
	if !found {
		return Window{}, fmt.Errorf("maintenance window %q: expected HH:MM-HH:MM", spec)
	}
	start, err := parseHHMM(from)
	if err != nil {
		return Window{}, fmt.Errorf("maintenance window %q: %w", spec, err)
	}
	end, err := parseHHMM(to)
	if err != nil {
		return Window{}, fmt.Errorf("maintenance window %q: %w", spec, err)
	}
	if start == end {
		return Window{}, fmt.Errorf("maintenance window %q: zero-length", spec)
	}
	return Window{start: start, end: end}, nil
}

func parseHHMM(s string) (time.Duration, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, err
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

// Contains reports whether t falls inside the window.
func (w Window) Contains(t time.Time) bool {
	if w.always {
		return true
	}
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	offset := t.Sub(midnight)
	if w.start < w.end {
		return offset >= w.start && offset < w.end
	}
	// Crosses midnight.
	return offset >= w.start || offset < w.end
}
