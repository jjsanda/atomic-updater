package rollout

import "testing"

func TestNormalizeVersion(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"1.0.0", "1.0.0", false},
		{"v1.2.3", "1.2.3", false},
		{" 1.2.3 ", "1.2.3", false},
		{"1.2", "1.2.0", false}, // canonicalized
		{"1.2.3-rc.1", "1.2.3-rc.1", false},
		{"", "", true},
		{"not-a-version", "", true},
		{"1.2.3.4", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeVersion(c.in)
		if c.wantErr != (err != nil) {
			t.Errorf("NormalizeVersion(%q): err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("NormalizeVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	if CompareVersions("1.0.0", "1.1.0") >= 0 {
		t.Error("1.0.0 should be < 1.1.0")
	}
	if CompareVersions("1.1.0", "1.1.0") != 0 {
		t.Error("equal versions should compare 0")
	}
	if CompareVersions("2.0.0", "1.9.9") <= 0 {
		t.Error("2.0.0 should be > 1.9.9")
	}
	if CompareVersions("1.0.0-rc.1", "1.0.0") >= 0 {
		t.Error("prerelease sorts before release")
	}
}
