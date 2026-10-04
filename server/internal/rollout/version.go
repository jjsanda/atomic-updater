package rollout

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// NormalizeVersion validates a semver string (optional leading 'v' tolerated)
// and returns its canonical form without the 'v' prefix, e.g. "1.2.0".
func NormalizeVersion(v string) (string, error) {
	s := strings.TrimSpace(v)
	if s == "" {
		return "", fmt.Errorf("empty version")
	}
	if !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	if !semver.IsValid(s) {
		return "", fmt.Errorf("invalid semver %q", v)
	}
	return strings.TrimPrefix(semver.Canonical(s), "v"), nil
}

// CompareVersions returns -1/0/+1 for canonical (or 'v'-prefixed) versions.
func CompareVersions(a, b string) int {
	if !strings.HasPrefix(a, "v") {
		a = "v" + a
	}
	if !strings.HasPrefix(b, "v") {
		b = "v" + b
	}
	return semver.Compare(a, b)
}
