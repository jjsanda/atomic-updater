package api

import (
	"context"
	"net/http"
	"time"

	"atomic-updater/server/internal/auth"
	"atomic-updater/server/internal/model"
)

type ctxKey int

const (
	peerKey ctxKey = iota
	deviceKey
)

// PeerFromContext / DeviceFromContext are used by handlers (and set directly
// by handler-level tests).
func PeerFromContext(ctx context.Context) (auth.PeerInfo, bool) {
	p, ok := ctx.Value(peerKey).(auth.PeerInfo)
	return p, ok
}

func DeviceFromContext(ctx context.Context) (*model.Device, bool) {
	d, ok := ctx.Value(deviceKey).(*model.Device)
	return d, ok
}

func ContextWithPeer(ctx context.Context, p auth.PeerInfo) context.Context {
	return context.WithValue(ctx, peerKey, p)
}

func ContextWithDevice(ctx context.Context, d *model.Device) context.Context {
	return context.WithValue(ctx, deviceKey, d)
}

// requirePeer classifies the TLS client certificate, rejects revoked
// fingerprints, and — for device peers — resolves and attaches the device row.
func (s *Server) requirePeer(kind auth.CertKind, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, ok := PeerFromContext(r.Context())
		if !ok {
			p, err := auth.ClassifyPeer(r.TLS, s.DeviceCA, s.MfgCA)
			if err != nil {
				writeError(w, http.StatusForbidden, "client certificate required")
				return
			}
			peer = p
		}
		revoked, err := s.Store.IsRevoked(r.Context(), peer.Fingerprint)
		if err != nil {
			s.Log.Error("revocation check failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if revoked {
			writeError(w, http.StatusForbidden, "certificate revoked")
			return
		}
		if peer.Kind != kind {
			writeError(w, http.StatusForbidden, "wrong certificate type for this endpoint")
			return
		}
		ctx := ContextWithPeer(r.Context(), peer)
		if kind == auth.KindDevice {
			if _, ok := DeviceFromContext(ctx); !ok {
				dev, err := s.Store.GetDeviceByFingerprint(ctx, peer.Fingerprint)
				if err != nil {
					s.Log.Error("device lookup failed", "err", err)
					writeError(w, http.StatusInternalServerError, "internal error")
					return
				}
				if dev == nil {
					writeError(w, http.StatusForbidden, "unknown device certificate")
					return
				}
				ctx = ContextWithDevice(ctx, dev)
			}
			if dev, _ := DeviceFromContext(ctx); dev != nil && dev.Status == "retired" {
				writeError(w, http.StatusGone, "device retired")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

const sessionCookie = "atomic_session"

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		ok, err := s.Sessions.Validate(r.Context(), c.Value)
		if err != nil {
			s.Log.Error("session validation failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "session expired")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// instrument adds panic recovery and request logging.
func (s *Server) instrument(listener string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if rec := recover(); rec != nil {
				s.Log.Error("panic in handler", "listener", listener,
					"method", r.Method, "path", r.URL.Path, "panic", rec)
				if !sw.wrote {
					writeError(sw, http.StatusInternalServerError, "internal error")
				}
				return
			}
			s.Log.Debug("request", "listener", listener, "method", r.Method,
				"path", r.URL.Path, "status", sw.status, "dur", time.Since(start))
		}()
		next.ServeHTTP(sw, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}
