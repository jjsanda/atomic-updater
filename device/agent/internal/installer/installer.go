// Package installer abstracts `rauc install` so the loop is testable and the
// e2e fallback tier can swap in a script (docs/security.md, degraded mode).
package installer

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Installer interface {
	// Install writes the bundle into the inactive slot and flips the boot
	// order. Combined output is returned for logging either way.
	Install(ctx context.Context, bundlePath string) (output string, err error)
}

// RaucCLI shells out to the rauc binary (PATH-resolved so the e2e image and
// tests can inject their own).
type RaucCLI struct {
	Binary    string        // default "rauc"
	ExtraArgs []string      // e.g. --override-boot-slot=A in the simulator
	Timeout   time.Duration // default 10m
}

func (r *RaucCLI) Install(ctx context.Context, bundlePath string) (string, error) {
	binary := r.Binary
	if binary == "" {
		binary = "rauc"
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append([]string{"install"}, r.ExtraArgs...)
	args = append(args, bundlePath)
	cmd := exec.CommandContext(ctx, binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w", binary, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// Command runs an arbitrary install command (config install_cmd) with the
// bundle path appended — the fallback installer tier uses this.
type Command struct {
	Line    string // e.g. "/usr/lib/atomic-updater/rauc-install-fallback.sh"
	Timeout time.Duration
}

func (c *Command) Install(ctx context.Context, bundlePath string) (string, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", c.Line+" "+shellQuote(bundlePath))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("install_cmd: %w", err)
	}
	return string(out), nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
