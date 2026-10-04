// Package api exposes the device REST API (mTLS) and the admin REST API +
// dashboard (session auth). Contract: docs/api.md.
package api

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"atomic-updater/server/internal/auth"
	"atomic-updater/server/internal/ca"
	"atomic-updater/server/internal/model"
	"atomic-updater/server/internal/rollout"
	"atomic-updater/server/internal/storage"
)

// Store is everything the handlers need from Postgres. *storage.DB
// implements it; tests substitute a fake.
type Store interface {
	rollout.Store
	auth.SessionStore

	GetDevice(ctx context.Context, id string) (*model.Device, error)
	GetDeviceByFingerprint(ctx context.Context, fp string) (*model.Device, error)
	GetDeviceByPubkey(ctx context.Context, fp string) (*model.Device, error)
	CreateDevice(ctx context.Context, d *model.Device) error
	UpdateDeviceCert(ctx context.Context, id, certFingerprint string) error
	UpdateDeviceCheckin(ctx context.Context, id, slot, version string,
		bundleID *string, bootCount int64, healthy bool, outcome string) error
	SetDeviceOutcome(ctx context.Context, id, outcome string) error
	ListDevices(ctx context.Context, f storage.DeviceFilter) ([]model.Device, int64, error)
	UpdateDeviceAdmin(ctx context.Context, id string, channel, status *string) (*model.Device, error)
	Summary(ctx context.Context) (*model.Summary, error)

	ListBundles(ctx context.Context) ([]model.Bundle, error)
	GetBundleByKey(ctx context.Context, key string) (*model.Bundle, error)
	InsertBundle(ctx context.Context, b *model.Bundle) error
	UpdateBundleStatus(ctx context.Context, id, status string) (*model.Bundle, error)

	ListRollouts(ctx context.Context) ([]model.Rollout, error)
	GetRollout(ctx context.Context, id string) (*model.Rollout, error)
	CreateRollout(ctx context.Context, r *model.Rollout) (*model.Rollout, error)
	UpdateRolloutPercentage(ctx context.Context, id string, pct int) (*model.Rollout, error)
	TransitionRollout(ctx context.Context, id, to string, allowedFrom []string) (*model.Rollout, error)
	EvaluateAutoPause(ctx context.Context, rolloutID string, minCohort, failPct int) (bool, error)
	RolloutStats(ctx context.Context, id string) (*model.RolloutStats, error)

	InsertEvent(ctx context.Context, deviceID, eventType string, payload json.RawMessage) error
	ListDeviceEvents(ctx context.Context, deviceID string, limit int) ([]model.Event, error)

	IsRevoked(ctx context.Context, certFingerprint string) (bool, error)
	RevokeCert(ctx context.Context, certFingerprint, serial, reason string) error

	Ping(ctx context.Context) error
}

// ObjectStore is what the admin handlers need from S3.
type ObjectStore interface {
	StatObject(ctx context.Context, key string) (int64, error)
	GetObjectBytes(ctx context.Context, key string, maxSize int64) ([]byte, error)
	Ping(ctx context.Context) error
}

// Scanner triggers a bundle-registration pass (bundles.Poller).
type Scanner interface {
	RunOnce(ctx context.Context) (int, error)
}

type Server struct {
	Store      Store
	Objects    ObjectStore
	Controller *rollout.Controller
	Issuer     *ca.Issuer
	Sessions   *auth.Sessions
	Scanner    Scanner // nil disables /admin/v1/bundles/scan

	DeviceCA   *x509.Certificate
	MfgCA      *x509.Certificate
	CAChainPEM []byte

	PauseMinCohort int
	PauseFailPct   int

	Log *slog.Logger
}

// DeviceHandler is mounted on the mTLS listener.
func (s *Server) DeviceHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/enroll", s.requirePeer(auth.KindManufacturing, http.HandlerFunc(s.handleEnroll)))
	mux.Handle("POST /api/v1/checkin", s.requirePeer(auth.KindDevice, http.HandlerFunc(s.handleCheckin)))
	mux.Handle("POST /api/v1/events", s.requirePeer(auth.KindDevice, http.HandlerFunc(s.handleEvents)))
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	return s.instrument("device", mux)
}

// AdminHandler is mounted on the plain listener; spa may be nil (dev).
func (s *Server) AdminHandler(spa http.Handler) http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /admin/v1/summary", s.handleSummary)
	api.HandleFunc("GET /admin/v1/devices", s.handleListDevices)
	api.HandleFunc("GET /admin/v1/devices/{id}", s.handleGetDevice)
	api.HandleFunc("GET /admin/v1/devices/{id}/events", s.handleDeviceEvents)
	api.HandleFunc("PATCH /admin/v1/devices/{id}", s.handlePatchDevice)
	api.HandleFunc("POST /admin/v1/devices/{id}/revoke", s.handleRevokeDevice)
	api.HandleFunc("GET /admin/v1/bundles", s.handleListBundles)
	api.HandleFunc("POST /admin/v1/bundles", s.handleRegisterBundle)
	api.HandleFunc("POST /admin/v1/bundles/scan", s.handleScanBundles)
	api.HandleFunc("PATCH /admin/v1/bundles/{id}", s.handlePatchBundle)
	api.HandleFunc("GET /admin/v1/rollouts", s.handleListRollouts)
	api.HandleFunc("POST /admin/v1/rollouts", s.handleCreateRollout)
	api.HandleFunc("PATCH /admin/v1/rollouts/{id}", s.handlePatchRollout)
	api.HandleFunc("GET /admin/v1/rollouts/{id}/stats", s.handleRolloutStats)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/v1/login", s.handleLogin)
	mux.HandleFunc("POST /admin/v1/logout", s.handleLogout)
	mux.Handle("/admin/v1/", s.requireSession(api))
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	if spa != nil {
		mux.Handle("/", spa)
	}
	return s.instrument("admin", mux)
}

// --- shared helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// decodeJSON enforces a body size cap and single-document strictness.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON: "+err.Error())
		return false
	}
	return true
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unreachable"})
		return
	}
	if err := s.Objects.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "object store unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
