// atomic-agent: the device check-in agent (design doc §7.5).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"atomic-updater/agent/internal/agent"
	"atomic-updater/agent/internal/client"
	"atomic-updater/agent/internal/config"
	"atomic-updater/agent/internal/enroll"
	"atomic-updater/agent/internal/installer"
	"atomic-updater/agent/internal/state"
	"atomic-updater/agent/internal/window"
)

func main() {
	configPath := flag.String("config", "/etc/atomic-updater/agent.conf", "config file path ('' to use env only)")
	once := flag.Bool("once", false, "run startup + a single check-in, then exit")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*configPath, *once, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(configPath string, once bool, log *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := state.Load(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}

	// First-boot enrollment (§11.3). Retry with backoff: a device that
	// cannot enroll yet must keep trying, not crash-loop.
	if !enroll.Enrolled(cfg.CertFile, cfg.KeyFile) {
		if cfg.MfgCertFile == "" || cfg.MfgKeyFile == "" {
			return fmt.Errorf("device not enrolled and no manufacturing certificate configured")
		}
		backoff := 5 * time.Second
		for {
			deviceID, err := enroll.Run(enroll.Params{
				ServerURL:    cfg.ServerURL,
				Keyring:      cfg.Keyring,
				MfgCertFile:  cfg.MfgCertFile,
				MfgKeyFile:   cfg.MfgKeyFile,
				CertFile:     cfg.CertFile,
				KeyFile:      cfg.KeyFile,
				HardwareType: cfg.HardwareType,
				Serial:       cfg.Serial,
				HTTPTimeout:  cfg.HTTPTimeout,
			})
			if err == nil {
				st.DeviceID = deviceID
				if err := st.Save(cfg.DataDir); err != nil {
					return err
				}
				log.Info("enrolled", "device_id", deviceID)
				break
			}
			log.Warn("enrollment failed, retrying", "err", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			if backoff < 2*time.Minute {
				backoff *= 2
			}
		}
	}

	api, err := client.New(cfg.ServerURL, cfg.Keyring, cfg.CertFile, cfg.KeyFile, cfg.HTTPTimeout)
	if err != nil {
		return err
	}

	var inst installer.Installer
	if cfg.InstallCmd != "" {
		inst = &installer.Command{Line: cfg.InstallCmd}
	} else {
		inst = &installer.RaucCLI{ExtraArgs: strings.Fields(cfg.RaucExtraArgs)}
	}

	win, err := window.Parse(cfg.MaintenanceWindow)
	if err != nil {
		return err
	}

	loop := &agent.Loop{
		Cfg:       cfg,
		API:       api,
		Installer: inst,
		Download:  &http.Client{Timeout: 15 * time.Minute},
		Window:    win,
		St:        st,
		Log:       log,
		Now:       time.Now,
		BootID:    func() (string, error) { return readBootID(cfg.BootIDFile) },
		Slot:      func() (string, error) { return detectSlot(cfg) },
		Version:   func() (string, error) { return readVersion(cfg.VersionFile) },
		Uptime:    readUptime,
		Reboot: func() error {
			return exec.Command("/bin/sh", "-c", cfg.RebootCmd).Run()
		},
	}

	if err := loop.Startup(ctx); err != nil {
		return err
	}
	if once {
		return loop.Tick(ctx)
	}
	return loop.Run(ctx)
}

func readBootID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// detectSlot: config override (also settable via ATOMIC_AGENT_SLOT) first,
// then the rauc.slot= kernel parameter set by the bootloader (§7.2).
func detectSlot(cfg *config.Config) (string, error) {
	if cfg.Slot != "" {
		return cfg.Slot, nil
	}
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return "", err
	}
	for _, field := range strings.Fields(string(data)) {
		if v, ok := strings.CutPrefix(field, "rauc.slot="); ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("no slot configured and no rauc.slot= on the kernel cmdline")
}

func readVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("version file: %w", err)
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return "", fmt.Errorf("version file %s is empty", path)
	}
	return v, nil
}

func readUptime() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return int64(seconds)
}
