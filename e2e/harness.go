// Package e2e drives the full OTA suite: compose infrastructure (Postgres,
// MinIO, atomic-server), signed test bundles, and containerized device
// simulators running the real RAUC + atomic-health + atomic-agent stack.
//
// Everything is stdlib-only and shells out to docker. Gated behind
// ATOMIC_E2E=1 (see Makefile `e2e` target).
package e2e

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	adminURL   = "http://127.0.0.1:18080"
	deviceURL  = "https://127.0.0.1:18443"
	simImage   = "atomic-updater/device-sim:dev"
	toolsImage = "atomic-updater/rauc-tools:dev"
	network    = "atomic-e2e_default"
	simLabel   = "atomic-e2e-sim"
)

type Harness struct {
	Root      string // repo root
	Tier      string // verity | plain | fallback
	Format    string // bundle format matching the tier
	BundleDir string
	Admin     *AdminClient
	Log       func(format string, args ...any)
}

var h *Harness

// --- process helpers ---

func run(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func (h *Harness) mustRun(dir, name string, args ...string) string {
	out, err := run(dir, name, args...)
	if err != nil {
		h.Log("command failed: %s %s\n%s", name, strings.Join(args, " "), out)
		panic(fmt.Sprintf("%s %s: %v", name, strings.Join(args, " "), err))
	}
	return out
}

// --- lifecycle (called from TestMain) ---

func setupHarness() (*Harness, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(cwd)
	hh := &Harness{
		Root:      root,
		BundleDir: filepath.Join(root, "out", "bundles"),
		Log: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "[harness] "+format+"\n", args...)
		},
	}

	hh.Log("bootstrapping dev PKI (idempotent)")
	if out, err := run(root, "scripts/pki/bootstrap-dev-pki.sh"); err != nil {
		return nil, fmt.Errorf("pki bootstrap: %v\n%s", err, out)
	}

	hh.Log("building rauc-tools + device-sim images")
	if out, err := run(root, "docker", "build", "-t", toolsImage, "bundle/"); err != nil {
		return nil, fmt.Errorf("rauc-tools build: %v\n%s", err, tail(out, 2000))
	}
	if out, err := run(root, "docker", "build", "-t", simImage,
		"-f", "e2e/device-sim/Dockerfile", "."); err != nil {
		return nil, fmt.Errorf("device-sim build: %v\n%s", err, tail(out, 2000))
	}

	hh.Log("starting compose stack (postgres, minio, atomic-server)")
	if out, err := run(root, "docker", "compose", "-f", "e2e/compose.yaml",
		"up", "-d", "--build"); err != nil {
		return nil, fmt.Errorf("compose up: %v\n%s", err, tail(out, 3000))
	}
	if err := hh.waitServerHealthy(2 * time.Minute); err != nil {
		out, _ := run(root, "docker", "compose", "-f", "e2e/compose.yaml", "logs", "server")
		return nil, fmt.Errorf("server never became healthy: %v\nserver logs:\n%s", err, tail(out, 3000))
	}

	if err := hh.probeTier(); err != nil {
		return nil, fmt.Errorf("capability probe: %w", err)
	}
	hh.Log("capability tier: %s (bundle format: %s)", hh.Tier, hh.Format)

	hh.Log("building test bundles (format %s)", hh.Format)
	if out, err := run(root, "e2e/make-test-bundles.sh", hh.BundleDir, hh.Format); err != nil {
		return nil, fmt.Errorf("test bundles: %v\n%s", err, tail(out, 2000))
	}
	if err := hh.UploadBundles(); err != nil {
		return nil, err
	}

	admin, err := NewAdminClient(adminURL, "admin", "admin")
	if err != nil {
		return nil, fmt.Errorf("admin login: %w", err)
	}
	hh.Admin = admin
	if n, err := admin.Scan(); err != nil {
		return nil, fmt.Errorf("bundle scan: %w", err)
	} else {
		hh.Log("bundle scan registered %d bundles", n)
	}
	return hh, nil
}

func (h *Harness) teardown() {
	out, _ := run(h.Root, "docker", "ps", "-aq", "--filter", "label="+simLabel)
	for _, id := range strings.Fields(out) {
		_, _ = run(h.Root, "docker", "rm", "-f", id)
	}
	if os.Getenv("ATOMIC_E2E_KEEP") == "" {
		_, _ = run(h.Root, "docker", "compose", "-f", "e2e/compose.yaml",
			"down", "-v", "--remove-orphans")
	} else {
		h.Log("ATOMIC_E2E_KEEP set: leaving compose stack running")
	}
}

func (h *Harness) waitServerHealthy(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(adminURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout after %s", timeout)
}

// probeTier determines how much privilege the Docker daemon grants us and
// picks the strongest workable install path:
//
//	verity   — privileged + host /dev: full `rauc install` of verity bundles
//	plain    — privileged, container /dev: loop mounts but no dm-verity
//	fallback — no privileges: signature-verified unpack installer
func (h *Harness) probeTier() error {
	if tier := os.Getenv("ATOMIC_E2E_TIER"); tier != "" {
		h.Tier = tier
		h.Format = map[string]string{"verity": "verity", "plain": "plain", "fallback": "verity"}[tier]
		if h.Format == "" {
			return fmt.Errorf("unknown ATOMIC_E2E_TIER %q", tier)
		}
		h.Log("tier forced by ATOMIC_E2E_TIER")
		return nil
	}

	probeDir, err := os.MkdirTemp("", "atomic-probe-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(probeDir)
	_ = os.Chmod(probeDir, 0o755)

	build := func(format string) error {
		out, err := run(h.Root, "docker", "run", "--rm",
			"-v", h.Root+"/pki:/pki:ro", "-v", probeDir+":/out", toolsImage,
			"make-bundle.sh", "--version", "0.0.1", "--hardware", "x86_64-generic",
			"--pki", "/pki", "--out-dir", "/out", "--format", format)
		if err != nil {
			return fmt.Errorf("probe bundle (%s): %v\n%s", format, err, tail(out, 1000))
		}
		return nil
	}
	install := func(dockerArgs ...string) bool {
		args := []string{"run", "--rm"}
		args = append(args, dockerArgs...)
		args = append(args,
			"-v", h.Root+"/pki:/pki:ro",
			"-v", h.Root+"/e2e/device-sim:/sim:ro",
			"-v", probeDir+":/bundles:ro",
			"-v", "/data", // anonymous volume: host-fs backed (plain bundles refuse overlayfs)
			toolsImage, "bash", "-c", `
set -e
mkdir -p /etc/rauc /usr/lib/atomic-updater /data/boot /data/slots /data/rauc
cp /sim/system.conf /etc/rauc/system.conf
cp /pki/keyring.pem /etc/rauc/ca.cert.pem
cp /sim/sim-env.sh /usr/lib/atomic-updater/
cp /sim/custom-backend.sh /usr/bin/ && cp /sim/fw_setenv /sim/fw_printenv /usr/bin/
chmod +x /usr/bin/custom-backend.sh /usr/bin/fw_setenv /usr/bin/fw_printenv
printf 'BOOT_ORDER=A B\nBOOT_A_LEFT=3\nBOOT_B_LEFT=3\n' > /data/boot/uboot-env
truncate -s 64M /data/slots/rootfs-A.img /data/slots/rootfs-B.img
cp /bundles/acme-device-v1-x86_64-generic-0.0.1.raucb /data/pending.raucb
rauc install --override-boot-slot=A /data/pending.raucb`)
		out, err := run(h.Root, "docker", args...)
		if err != nil {
			h.Log("probe install failed (%s): %s", strings.Join(dockerArgs, " "), tail(out, 500))
		}
		return err == nil
	}

	if err := build("verity"); err != nil {
		return err
	}
	if install("--privileged", "-v", "/dev:/dev") {
		h.Tier, h.Format = "verity", "verity"
		return nil
	}
	h.Log("verity tier unavailable, probing plain")
	if err := build("plain"); err != nil {
		return err
	}
	if install("--privileged") {
		h.Tier, h.Format = "plain", "plain"
		return nil
	}
	h.Log("plain tier unavailable, falling back to unprivileged installer (DEGRADED)")
	h.Tier, h.Format = "fallback", "verity"
	return nil
}

func (h *Harness) UploadBundles() error {
	h.Log("uploading bundles to MinIO signed/ prefix")
	out, err := run(h.Root, "docker", "run", "--rm", "--network", network,
		"-v", h.BundleDir+":/b:ro", "--entrypoint", "sh", "minio/mc:latest", "-c",
		`mc alias set local http://minio:9000 atomicdev atomicdev-secret >/dev/null &&
		 mc mb -p local/bundles >/dev/null 2>&1 || true &&
		 mc cp --recursive /b/ local/bundles/signed/`)
	if err != nil {
		return fmt.Errorf("mc upload: %v\n%s", err, tail(out, 1500))
	}
	return nil
}

// BuildAndUploadBundle creates one extra bundle (soak) and uploads it.
func (h *Harness) BuildAndUploadBundle(version string, extraArgs ...string) error {
	args := []string{"run", "--rm",
		"-v", h.Root + "/pki:/pki:ro", "-v", h.BundleDir + ":/out", toolsImage,
		"make-bundle.sh", "--version", version, "--hardware", "x86_64-generic",
		"--pki", "/pki", "--out-dir", "/out", "--format", h.Format}
	args = append(args, extraArgs...)
	if out, err := run(h.Root, "docker", args...); err != nil {
		return fmt.Errorf("bundle %s: %v\n%s", version, err, tail(out, 1000))
	}
	return h.UploadBundles()
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// --- admin API client ---

type AdminClient struct {
	base string
	http *http.Client
}

type Bundle struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	HardwareType string `json:"hardware_type"`
	ObjectKey    string `json:"object_key"`
	Status       string `json:"status"`
}

type Rollout struct {
	ID            string  `json:"id"`
	BundleID      string  `json:"bundle_id"`
	BundleVersion string  `json:"bundle_version"`
	Channel       string  `json:"channel"`
	Percentage    int     `json:"percentage"`
	Status        string  `json:"status"`
	PausedReason  *string `json:"paused_reason"`
}

type Device struct {
	ID                string     `json:"id"`
	Serial            string     `json:"serial"`
	Channel           string     `json:"channel"`
	Status            string     `json:"status"`
	CurrentVersion    string     `json:"current_version"`
	CurrentSlot       string     `json:"current_slot"`
	BootCount         int64      `json:"boot_count"`
	LastUpdateOutcome string     `json:"last_update_outcome"`
	LastSeenAt        *time.Time `json:"last_seen_at"`
}

type Event struct {
	ID        int64           `json:"id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

func NewAdminClient(base, user, password string) (*AdminClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	c := &AdminClient{base: base, http: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
	return c, c.do("POST", "/admin/v1/login",
		map[string]string{"username": user, "password": password}, nil)
}

func (c *AdminClient) do(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d: %s", method, path, resp.StatusCode, tail(string(data), 300))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *AdminClient) Scan() (int, error) {
	var resp struct {
		Registered int `json:"registered"`
	}
	err := c.do("POST", "/admin/v1/bundles/scan", nil, &resp)
	return resp.Registered, err
}

func (c *AdminClient) Bundles() ([]Bundle, error) {
	var resp struct {
		Bundles []Bundle `json:"bundles"`
	}
	err := c.do("GET", "/admin/v1/bundles", nil, &resp)
	return resp.Bundles, err
}

func (c *AdminClient) BundleByVersion(version string) (*Bundle, error) {
	bundles, err := c.Bundles()
	if err != nil {
		return nil, err
	}
	for i := range bundles {
		if bundles[i].Version == version {
			return &bundles[i], nil
		}
	}
	return nil, fmt.Errorf("no registered bundle with version %s", version)
}

// StartRollout creates and immediately activates a rollout.
func (c *AdminClient) StartRollout(bundleID, channel string, pct int, allowDowngrade bool) (*Rollout, error) {
	var ro Rollout
	err := c.do("POST", "/admin/v1/rollouts", map[string]any{
		"bundle_id": bundleID, "channel": channel,
		"percentage": pct, "allow_downgrade": allowDowngrade,
	}, &ro)
	if err != nil {
		return nil, err
	}
	if err := c.do("PATCH", "/admin/v1/rollouts/"+ro.ID,
		map[string]string{"status": "active"}, &ro); err != nil {
		return nil, err
	}
	return &ro, nil
}

func (c *AdminClient) GetRollout(id string) (*Rollout, error) {
	rollouts, err := c.Rollouts()
	if err != nil {
		return nil, err
	}
	for i := range rollouts {
		if rollouts[i].ID == id {
			return &rollouts[i], nil
		}
	}
	return nil, fmt.Errorf("rollout %s not found", id)
}

func (c *AdminClient) Rollouts() ([]Rollout, error) {
	var resp struct {
		Rollouts []Rollout `json:"rollouts"`
	}
	err := c.do("GET", "/admin/v1/rollouts", nil, &resp)
	return resp.Rollouts, err
}

func (c *AdminClient) PatchRollout(id string, patch map[string]any) error {
	return c.do("PATCH", "/admin/v1/rollouts/"+id, patch, nil)
}

// AbortActiveRollouts stops anything active or paused (test isolation).
func (c *AdminClient) AbortActiveRollouts() error {
	rollouts, err := c.Rollouts()
	if err != nil {
		return err
	}
	for _, ro := range rollouts {
		if ro.Status == "active" || ro.Status == "paused" {
			if err := c.PatchRollout(ro.ID, map[string]any{"status": "aborted"}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *AdminClient) DeviceBySerial(serial string) (*Device, error) {
	var resp struct {
		Devices []Device `json:"devices"`
	}
	if err := c.do("GET", "/admin/v1/devices?q="+serial, nil, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Devices {
		if resp.Devices[i].Serial == serial {
			return &resp.Devices[i], nil
		}
	}
	return nil, fmt.Errorf("no device with serial %s", serial)
}

func (c *AdminClient) DeviceEvents(deviceID string, limit int) ([]Event, error) {
	var resp struct {
		Events []Event `json:"events"`
	}
	err := c.do("GET", fmt.Sprintf("/admin/v1/devices/%s/events?limit=%d", deviceID, limit), nil, &resp)
	return resp.Events, err
}

// --- device simulators ---

type Sim struct {
	Name string
	h    *Harness
}

// StartSim launches a device simulator wired to the compose network with the
// harness's capability tier.
func (h *Harness) StartSim(t *testing.T, name string, extraEnv map[string]string) *Sim {
	t.Helper()
	cname := "atomic-e2e-" + name
	args := []string{"run", "-d", "--name", cname, "--label", simLabel,
		"--network", network, "--hostname", name,
		"-v", "/data", // anonymous volume: keeps slots + bundle off overlayfs
		"-v", h.Root + "/pki/keyring.pem:/etc/rauc/ca.cert.pem:ro",
		"-v", h.Root + "/pki/manufacturing/mfg-fleet.cert.pem:/etc/atomic-updater/mfg.cert.pem:ro",
		"-v", h.Root + "/pki/manufacturing/mfg-fleet.key.pem:/etc/atomic-updater/mfg.key.pem:ro",
		"-e", "ATOMIC_AGENT_SERIAL=" + name,
	}
	switch h.Tier {
	case "verity":
		args = append(args, "--privileged", "-v", "/dev:/dev")
	case "plain":
		args = append(args, "--privileged")
	case "fallback":
		args = append(args, "-e",
			"ATOMIC_AGENT_INSTALL_CMD=/usr/lib/atomic-updater/rauc-install-fallback.sh")
	}
	for k, v := range extraEnv {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, simImage)
	if out, err := run(h.Root, "docker", args...); err != nil {
		t.Fatalf("start sim %s: %v\n%s", name, err, out)
	}
	sim := &Sim{Name: cname, h: h}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("=== logs of %s ===\n%s", cname, sim.Logs())
		}
		_, _ = run(h.Root, "docker", "rm", "-f", cname)
	})
	return sim
}

func (s *Sim) Exec(args ...string) (string, error) {
	return run(s.h.Root, "docker", append([]string{"exec", s.Name}, args...)...)
}

func (s *Sim) Logs() string {
	out, _ := run(s.h.Root, "docker", "logs", s.Name)
	return out
}

// BootEnv returns the simulated U-Boot environment.
func (s *Sim) BootEnv() map[string]string {
	out, _ := s.Exec("cat", "/data/boot/uboot-env")
	env := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			env[k] = v
		}
	}
	return env
}

func (s *Sim) RescueHalted() bool {
	_, err := s.Exec("test", "-e", "/data/RESCUE_HALT")
	return err == nil
}

// NetworkDisconnect / NetworkConnect implement mid-download chaos.
func (s *Sim) NetworkDisconnect() error {
	_, err := run(s.h.Root, "docker", "network", "disconnect", network, s.Name)
	return err
}

func (s *Sim) NetworkConnect() error {
	_, err := run(s.h.Root, "docker", "network", "connect", network, s.Name)
	return err
}

// ThrottleIngress rate-limits incoming traffic (tc ingress policing) so a
// bundle download is slow enough to interrupt. Requires NET_ADMIN
// (privileged tiers).
func (s *Sim) ThrottleIngress(rate string) error {
	if _, err := s.Exec("tc", "qdisc", "add", "dev", "eth0", "handle", "ffff:", "ingress"); err != nil {
		return err
	}
	_, err := s.Exec("tc", "filter", "add", "dev", "eth0", "parent", "ffff:",
		"protocol", "ip", "u32", "match", "u32", "0", "0",
		"police", "rate", rate, "burst", "64k", "drop")
	return err
}

// --- assertions ---

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		ok, status := cond()
		if ok {
			return
		}
		last = status
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timeout (%s) waiting for %s; last state: %s", timeout, what, last)
}

// waitDevice polls the admin API for the device with the given serial until
// cond is satisfied.
func (h *Harness) waitDevice(t *testing.T, serial, what string, timeout time.Duration,
	cond func(*Device) bool) *Device {
	t.Helper()
	var found *Device
	waitFor(t, fmt.Sprintf("device %s: %s", serial, what), timeout, func() (bool, string) {
		dev, err := h.Admin.DeviceBySerial(serial)
		if err != nil {
			return false, err.Error()
		}
		found = dev
		return cond(dev), fmt.Sprintf("version=%s slot=%s outcome=%s boots=%d",
			dev.CurrentVersion, dev.CurrentSlot, dev.LastUpdateOutcome, dev.BootCount)
	})
	return found
}

func eventTypes(events []Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.EventType)
	}
	return out
}

func hasEvent(events []Event, eventType, payloadContains string) bool {
	for _, e := range events {
		if e.EventType == eventType &&
			(payloadContains == "" || strings.Contains(string(e.Payload), payloadContains)) {
			return true
		}
	}
	return false
}

// --- synthetic device client (auto-pause test enrolls devices in-process) ---

type SyntheticDevice struct {
	Serial   string
	DeviceID string
	client   *http.Client
}

// EnrollSynthetic creates a device identity over the real mTLS enrollment
// flow using the manufacturing certificate, entirely in-process.
func (h *Harness) EnrollSynthetic(serial string) (*SyntheticDevice, error) {
	roots := x509.NewCertPool()
	keyring, err := os.ReadFile(filepath.Join(h.Root, "pki", "keyring.pem"))
	if err != nil {
		return nil, err
	}
	roots.AppendCertsFromPEM(keyring)

	mfgCert, err := tls.LoadX509KeyPair(
		filepath.Join(h.Root, "pki", "manufacturing", "mfg-fleet.cert.pem"),
		filepath.Join(h.Root, "pki", "manufacturing", "mfg-fleet.key.pem"))
	if err != nil {
		return nil, err
	}
	mfgClient := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:      roots,
			Certificates: []tls.Certificate{mfgCert},
			ServerName:   "localhost",
		},
	}}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "enrollment:" + serial},
	}, key)
	if err != nil {
		return nil, err
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	body, _ := json.Marshal(map[string]string{
		"csr_pem": string(csrPEM), "hardware_type": "x86_64-generic", "serial": serial,
	})
	resp, err := mfgClient.Post(deviceURL+"/api/v1/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("enroll %s: %d: %s", serial, resp.StatusCode, tail(string(data), 200))
	}
	var enrolled struct {
		DeviceID       string `json:"device_id"`
		CertificatePEM string `json:"certificate_pem"`
	}
	if err := json.Unmarshal(data, &enrolled); err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(enrolled.CertificatePEM))
	if block == nil {
		return nil, fmt.Errorf("no cert in enroll response")
	}
	devCert := tls.Certificate{Certificate: [][]byte{block.Bytes}, PrivateKey: key}

	return &SyntheticDevice{
		Serial:   serial,
		DeviceID: enrolled.DeviceID,
		client: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      roots,
				Certificates: []tls.Certificate{devCert},
				ServerName:   "localhost",
			},
		}},
	}, nil
}

func (d *SyntheticDevice) post(path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := d.client.Post(deviceURL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %d: %s", path, resp.StatusCode, tail(string(data), 200))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Checkin reports a plausible device state and returns the offered rollout id
// ("" when no update was assigned).
func (d *SyntheticDevice) Checkin(version string) (rolloutID string, err error) {
	var resp struct {
		Update *struct {
			RolloutID string `json:"rollout_id"`
			BundleID  string `json:"bundle_id"`
		} `json:"update"`
	}
	err = d.post("/api/v1/checkin", map[string]any{
		"slot": "A", "version": version, "bundle_id": nil,
		"boot_count": 1, "uptime_s": 60, "healthy": true,
		"last_update": map[string]any{"outcome": "none", "bundle_id": nil, "reason": ""},
	}, &resp)
	if err != nil {
		return "", err
	}
	if resp.Update != nil {
		return resp.Update.RolloutID, nil
	}
	return "", nil
}

func (d *SyntheticDevice) ReportInstallFail(rolloutID, bundleID string) error {
	payload, _ := json.Marshal(map[string]string{
		"rollout_id": rolloutID, "bundle_id": bundleID, "reason": "synthetic failure",
	})
	return d.post("/api/v1/events", map[string]any{
		"events": []map[string]any{{"event_type": "install-fail", "payload": json.RawMessage(payload)}},
	}, nil)
}
