package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// rangeServer serves payload honoring Range headers.
func rangeServer(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := int64(0)
		if h := r.Header.Get("Range"); h != "" {
			v := strings.TrimSuffix(strings.TrimPrefix(h, "bytes="), "-")
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n >= int64(len(payload)) {
				http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
				return
			}
			offset = n
			w.Header().Set("Content-Range", "bytes "+v+"-/"+strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write(payload[offset:])
	}))
}

func TestFetchFull(t *testing.T) {
	payload := []byte(strings.Repeat("bundle-data-", 1000))
	ts := rangeServer(t, payload)
	defer ts.Close()

	dest := filepath.Join(t.TempDir(), "pending.raucb")
	res, err := Fetch(context.Background(), ts.Client(), ts.URL, dest, sha(payload), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.ResumedFrom != 0 {
		t.Errorf("fresh download reported resume from %d", res.ResumedFrom)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(payload) {
		t.Error("content mismatch")
	}
}

func TestFetchResumesPartial(t *testing.T) {
	payload := []byte(strings.Repeat("bundle-data-", 1000))
	ts := rangeServer(t, payload)
	defer ts.Close()

	dest := filepath.Join(t.TempDir(), "pending.raucb")
	// Simulate an interrupted earlier attempt: half the payload on disk.
	half := int64(len(payload) / 2)
	if err := os.WriteFile(dest+".partial", payload[:half], 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Fetch(context.Background(), ts.Client(), ts.URL, dest, sha(payload), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.ResumedFrom != half {
		t.Errorf("expected resume from %d, got %d", half, res.ResumedFrom)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(payload) {
		t.Error("resumed content mismatch")
	}
}

func TestFetchRestartsWhenServerIgnoresRange(t *testing.T) {
	payload := []byte("full-payload-no-range-support")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload) // always 200, full body
	}))
	defer ts.Close()

	dest := filepath.Join(t.TempDir(), "pending.raucb")
	if err := os.WriteFile(dest+".partial", []byte("stale-junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Fetch(context.Background(), ts.Client(), ts.URL, dest, sha(payload), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.ResumedFrom != 0 {
		t.Error("200 response must restart, not resume")
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(payload) {
		t.Error("content mismatch after restart")
	}
}

// A server that stalls mid-body must trip the watchdog quickly, and the
// partial file must survive for the next resume attempt.
func TestFetchAbortsOnStall(t *testing.T) {
	payload := []byte(strings.Repeat("bundle-data-", 1000))
	half := len(payload) / 2
	stall := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload[:half])
		w.(http.Flusher).Flush()
		<-stall // never send the rest
	}))
	defer func() { close(stall); ts.Close() }()

	dest := filepath.Join(t.TempDir(), "pending.raucb")
	start := time.Now()
	_, err := Fetch(context.Background(), ts.Client(), ts.URL, dest,
		sha(payload), 300*time.Millisecond)
	if err == nil {
		t.Fatal("stalled download must error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("watchdog too slow: %s", elapsed)
	}
	info, statErr := os.Stat(dest + ".partial")
	if statErr != nil {
		t.Fatal("partial file must survive a stall abort")
	}
	if info.Size() == 0 {
		t.Error("partial should contain the bytes received before the stall")
	}
}

func TestFetchRejectsChecksumMismatch(t *testing.T) {
	payload := []byte("tampered-payload")
	ts := rangeServer(t, payload)
	defer ts.Close()

	dest := filepath.Join(t.TempDir(), "pending.raucb")
	_, err := Fetch(context.Background(), ts.Client(), ts.URL, dest,
		strings.Repeat("0", 64), 0)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch, got %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Error("corrupt file must not be renamed into place")
	}
	if _, statErr := os.Stat(dest + ".partial"); !os.IsNotExist(statErr) {
		t.Error("corrupt partial must be removed (resuming it can never succeed)")
	}
}
