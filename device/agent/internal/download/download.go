// Package download fetches bundles with HTTP Range resume, sha256
// verification, and a stall watchdog (design doc §13.2
// TestResumeAfterNetworkDrop): a link that silently dies mid-transfer must
// surface as an error promptly — not block until a multi-minute TCP timeout —
// so the poll loop can fetch a fresh URL and resume.
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

// Result describes a finished (or attempted) download.
type Result struct {
	Path        string
	ResumedFrom int64 // >0 when a partial file was continued
	Bytes       int64
}

// Fetch downloads url into dest (via dest.partial). A pre-existing partial
// file is resumed with a Range request when the server supports it. The
// finished file is renamed into place only after its sha256 matches.
// stallTimeout > 0 aborts the transfer when no bytes arrive for that long
// (the partial file survives for the next resume attempt).
func Fetch(ctx context.Context, client *http.Client, url, dest, wantSHA256 string,
	stallTimeout time.Duration) (*Result, error) {
	partial := dest + ".partial"
	res := &Result{Path: dest}

	var offset int64
	if info, err := os.Stat(partial); err == nil {
		offset = info.Size()
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return res, err
	}
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	resp, err := client.Do(req)
	if err != nil {
		return res, err // keep the partial file for the next attempt
	}
	defer resp.Body.Close()

	var file *os.File
	switch resp.StatusCode {
	case http.StatusPartialContent:
		res.ResumedFrom = offset
		file, err = os.OpenFile(partial, os.O_WRONLY|os.O_APPEND, 0o644)
	case http.StatusOK:
		// Server ignored the Range (or fresh download): start over.
		res.ResumedFrom = 0
		file, err = os.Create(partial)
	default:
		return res, fmt.Errorf("download: unexpected status %d", resp.StatusCode)
	}
	if err != nil {
		return res, err
	}

	var body io.Reader = resp.Body
	if stallTimeout > 0 {
		// The watchdog cancels the request context when a read makes no
		// progress for stallTimeout — a dead link then errors immediately
		// instead of hanging until the TCP stack gives up.
		watchdog := time.AfterFunc(stallTimeout, cancel)
		defer watchdog.Stop()
		body = &stallGuard{r: resp.Body, watchdog: watchdog, timeout: stallTimeout}
	}

	written, copyErr := io.Copy(file, body)
	closeErr := file.Close()
	res.Bytes = written
	if copyErr != nil {
		return res, fmt.Errorf("download interrupted after %d bytes: %w", written, copyErr)
	}
	if closeErr != nil {
		return res, closeErr
	}

	sum, err := fileSHA256(partial)
	if err != nil {
		return res, err
	}
	if sum != wantSHA256 {
		// A corrupt file can never become valid by resuming — remove it.
		_ = os.Remove(partial)
		return res, fmt.Errorf("sha256 mismatch: got %s, want %s", sum, wantSHA256)
	}
	if err := os.Rename(partial, dest); err != nil {
		return res, err
	}
	return res, nil
}

// stallGuard re-arms the watchdog after every successful read.
type stallGuard struct {
	r        io.Reader
	watchdog *time.Timer
	timeout  time.Duration
}

func (g *stallGuard) Read(p []byte) (int, error) {
	n, err := g.r.Read(p)
	if n > 0 {
		g.watchdog.Reset(g.timeout)
	}
	return n, err
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FileSHA256 is exported for pre-flight checks (skip re-download when the
// finished file already matches).
func FileSHA256(path string) (string, error) { return fileSHA256(path) }
