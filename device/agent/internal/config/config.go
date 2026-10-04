// Package config parses the agent's flat key=value configuration file
// (/etc/atomic-updater/agent.conf) with ATOMIC_AGENT_* env overrides.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	ServerURL string // https://atomic-server:8443

	// Trust + identity material.
	Keyring     string // org root CA (pins the server cert AND ships as RAUC keyring)
	CertFile    string // device cert (written at enrollment)
	KeyFile     string // device key (generated at enrollment)
	MfgCertFile string // manufacturing cert (enrollment only)
	MfgKeyFile  string

	HardwareType string
	Serial       string

	DataDir           string        // /data/atomic-updater
	VersionFile       string        // /etc/bundle-version inside the running rootfs
	BootIDFile        string        // /proc/sys/kernel/random/boot_id (simulator overrides)
	Slot              string        // override; else $ATOMIC_SLOT, else /proc/cmdline rauc.slot=
	PollInterval      time.Duration // 60s
	PollJitter        time.Duration // ±10s
	MaintenanceWindow string        // "" = anytime; "HH:MM-HH:MM" local
	RebootCmd         string        // "systemctl reboot"
	InstallCmd        string        // "" = use rauc CLI installer
	RaucExtraArgs     string        // e.g. "--override-boot-slot=A" in the simulator
	HTTPTimeout       time.Duration
	// Abort a bundle download when no bytes arrive for this long; the
	// partial file is kept and resumed on the next poll (0 disables).
	DownloadStallTimeout time.Duration
	// Transient install failures (e.g. a briefly busy device) are retried
	// before install-fail is reported — which per design §10.4 permanently
	// blocks the bundle for this device until operator action.
	InstallRetries    int
	InstallRetryDelay time.Duration
}

func Defaults() *Config {
	return &Config{
		DataDir:      "/data/atomic-updater",
		VersionFile:  "/etc/bundle-version",
		BootIDFile:   "/proc/sys/kernel/random/boot_id",
		PollInterval: 60 * time.Second,
		PollJitter:   10 * time.Second,
		RebootCmd:    "systemctl reboot",
		HTTPTimeout:  30 * time.Second,

		DownloadStallTimeout: 60 * time.Second,
		InstallRetries:       2,
		InstallRetryDelay:    10 * time.Second,
	}
}

// Load reads path (optional — "" skips the file), then applies env overrides.
func Load(path string) (*Config, error) {
	c := Defaults()
	if path != "" {
		if err := c.loadFile(path); err != nil {
			return nil, err
		}
	}
	c.applyEnv()
	if c.CertFile == "" {
		c.CertFile = filepath.Join(c.DataDir, "identity", "device.cert.pem")
	}
	if c.KeyFile == "" {
		c.KeyFile = filepath.Join(c.DataDir, "identity", "device.key.pem")
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) loadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, found := strings.Cut(text, "=")
		if !found {
			return fmt.Errorf("%s:%d: expected key=value", path, line)
		}
		if err := c.set(strings.TrimSpace(key), strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("%s:%d: %w", path, line, err)
		}
	}
	return scanner.Err()
}

func (c *Config) applyEnv() {
	for key, env := range map[string]string{
		"server_url":             "ATOMIC_AGENT_SERVER_URL",
		"keyring":                "ATOMIC_AGENT_KEYRING",
		"cert_file":              "ATOMIC_AGENT_CERT_FILE",
		"key_file":               "ATOMIC_AGENT_KEY_FILE",
		"mfg_cert_file":          "ATOMIC_AGENT_MFG_CERT_FILE",
		"mfg_key_file":           "ATOMIC_AGENT_MFG_KEY_FILE",
		"hardware_type":          "ATOMIC_AGENT_HARDWARE_TYPE",
		"serial":                 "ATOMIC_AGENT_SERIAL",
		"data_dir":               "ATOMIC_AGENT_DATA_DIR",
		"version_file":           "ATOMIC_AGENT_VERSION_FILE",
		"boot_id_file":           "ATOMIC_AGENT_BOOT_ID_FILE",
		"slot":                   "ATOMIC_AGENT_SLOT",
		"poll_interval":          "ATOMIC_AGENT_POLL_INTERVAL",
		"poll_jitter":            "ATOMIC_AGENT_POLL_JITTER",
		"maintenance_window":     "ATOMIC_AGENT_MAINTENANCE_WINDOW",
		"reboot_cmd":             "ATOMIC_AGENT_REBOOT_CMD",
		"install_cmd":            "ATOMIC_AGENT_INSTALL_CMD",
		"rauc_extra_args":        "ATOMIC_AGENT_RAUC_EXTRA_ARGS",
		"http_timeout":           "ATOMIC_AGENT_HTTP_TIMEOUT",
		"download_stall_timeout": "ATOMIC_AGENT_DOWNLOAD_STALL_TIMEOUT",
		"install_retries":        "ATOMIC_AGENT_INSTALL_RETRIES",
		"install_retry_delay":    "ATOMIC_AGENT_INSTALL_RETRY_DELAY",
	} {
		if v, ok := os.LookupEnv(env); ok {
			_ = c.set(key, v) // validated below
		}
	}
}

func (c *Config) set(key, value string) error {
	var err error
	switch key {
	case "server_url":
		c.ServerURL = value
	case "keyring":
		c.Keyring = value
	case "cert_file":
		c.CertFile = value
	case "key_file":
		c.KeyFile = value
	case "mfg_cert_file":
		c.MfgCertFile = value
	case "mfg_key_file":
		c.MfgKeyFile = value
	case "hardware_type":
		c.HardwareType = value
	case "serial":
		c.Serial = value
	case "data_dir":
		c.DataDir = value
	case "version_file":
		c.VersionFile = value
	case "boot_id_file":
		c.BootIDFile = value
	case "slot":
		c.Slot = value
	case "poll_interval":
		c.PollInterval, err = time.ParseDuration(value)
	case "poll_jitter":
		c.PollJitter, err = time.ParseDuration(value)
	case "maintenance_window":
		c.MaintenanceWindow = value
	case "reboot_cmd":
		c.RebootCmd = value
	case "install_cmd":
		c.InstallCmd = value
	case "rauc_extra_args":
		c.RaucExtraArgs = value
	case "http_timeout":
		c.HTTPTimeout, err = time.ParseDuration(value)
	case "download_stall_timeout":
		c.DownloadStallTimeout, err = time.ParseDuration(value)
	case "install_retries":
		_, err = fmt.Sscanf(value, "%d", &c.InstallRetries)
	case "install_retry_delay":
		c.InstallRetryDelay, err = time.ParseDuration(value)
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	return err
}

func (c *Config) validate() error {
	var missing []string
	for name, v := range map[string]string{
		"server_url":    c.ServerURL,
		"keyring":       c.Keyring,
		"hardware_type": c.HardwareType,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	if c.PollInterval <= 0 {
		return fmt.Errorf("poll_interval must be positive")
	}
	return nil
}
