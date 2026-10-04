package window

import (
	"testing"
	"time"
)

func at(hhmm string) time.Time {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		panic(err)
	}
	return time.Date(2026, 8, 13, t.Hour(), t.Minute(), 0, 0, time.Local)
}

func TestWindows(t *testing.T) {
	cases := []struct {
		spec string
		time string
		want bool
	}{
		{"", "12:00", true}, // empty = always
		{"02:00-04:00", "03:00", true},
		{"02:00-04:00", "02:00", true},  // inclusive start
		{"02:00-04:00", "04:00", false}, // exclusive end
		{"02:00-04:00", "01:59", false},
		{"02:00-04:00", "23:00", false},
		{"22:00-02:00", "23:30", true}, // crosses midnight
		{"22:00-02:00", "01:00", true},
		{"22:00-02:00", "12:00", false},
		{"22:00-02:00", "21:59", false},
		{"22:00-02:00", "02:00", false},
	}
	for _, c := range cases {
		w, err := Parse(c.spec)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.spec, err)
		}
		if got := w.Contains(at(c.time)); got != c.want {
			t.Errorf("window %q at %s: got %v, want %v", c.spec, c.time, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, spec := range []string{"2am-4am", "02:00", "02:00-02:00", "25:00-04:00"} {
		if _, err := Parse(spec); err == nil {
			t.Errorf("Parse(%q) should fail", spec)
		}
	}
}
