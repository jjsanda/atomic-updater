package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"atomic-updater/server/internal/auth"
	"atomic-updater/server/internal/bundles"
	"atomic-updater/server/internal/model"
	"atomic-updater/server/internal/storage"
)

// --- auth ---

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if !decodeJSON(w, r, &req, 4096) {
		return
	}
	token, err := s.Sessions.Login(r.Context(), req.Username, req.Password)
	if errors.Is(err, auth.ErrBadCredentials) {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if err != nil {
		s.Log.Error("login failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   int((24 * time.Hour).Seconds()),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.Sessions.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

// --- fleet ---

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	sum, err := s.Store.Summary(r.Context())
	if err != nil {
		s.Log.Error("summary failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := SummaryResponse{
		DevicesTotal: sum.DevicesTotal,
		ByChannel:    sum.ByChannel,
		ByVersion:    sum.ByVersion,
		MarkBadRate:  make([]MarkBadBucketJSON, 0, len(sum.MarkBad1h)),
	}
	for _, b := range sum.MarkBad1h {
		resp.MarkBadRate = append(resp.MarkBadRate, MarkBadBucketJSON{Bucket: b.Bucket, Count: b.Count})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	devs, total, err := s.Store.ListDevices(r.Context(), storage.DeviceFilter{
		Channel:      q.Get("channel"),
		HardwareType: q.Get("hardware_type"),
		Query:        q.Get("q"),
		Limit:        limit,
		Offset:       offset,
	})
	if err != nil {
		s.Log.Error("device list failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := DevicesListResponse{Devices: make([]DeviceJSON, 0, len(devs)), Total: total}
	for i := range devs {
		resp.Devices = append(resp.Devices, toDeviceJSON(&devs[i]))
	}
	writeJSON(w, http.StatusOK, resp)
}

// deviceByPathID resolves {id}, writing the error response on failure.
func (s *Server) deviceByPathID(w http.ResponseWriter, r *http.Request) *model.Device {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusBadRequest, "malformed device id")
		return nil
	}
	dev, err := s.Store.GetDevice(r.Context(), id)
	if err != nil {
		s.Log.Error("device lookup failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return nil
	}
	if dev == nil {
		writeError(w, http.StatusNotFound, "no such device")
		return nil
	}
	return dev
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	if dev := s.deviceByPathID(w, r); dev != nil {
		writeJSON(w, http.StatusOK, toDeviceJSON(dev))
	}
}

func (s *Server) handleDeviceEvents(w http.ResponseWriter, r *http.Request) {
	dev := s.deviceByPathID(w, r)
	if dev == nil {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.Store.ListDeviceEvents(r.Context(), dev.ID, limit)
	if err != nil {
		s.Log.Error("event list failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := EventsListResponse{Events: make([]EventJSON, 0, len(events))}
	for _, e := range events {
		resp.Events = append(resp.Events, EventJSON{
			ID: e.ID, EventType: e.EventType, Payload: e.Payload, CreatedAt: e.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePatchDevice(w http.ResponseWriter, r *http.Request) {
	dev := s.deviceByPathID(w, r)
	if dev == nil {
		return
	}
	var req PatchDeviceRequest
	if !decodeJSON(w, r, &req, 4096) {
		return
	}
	if req.Channel == nil && req.Status == nil {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	if req.Channel != nil && !model.Channels[*req.Channel] {
		writeError(w, http.StatusBadRequest, "unknown channel")
		return
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "retired" {
		writeError(w, http.StatusBadRequest, "status must be active or retired")
		return
	}
	updated, err := s.Store.UpdateDeviceAdmin(r.Context(), dev.ID, req.Channel, req.Status)
	if err != nil || updated == nil {
		s.Log.Error("device update failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, toDeviceJSON(updated))
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	dev := s.deviceByPathID(w, r)
	if dev == nil {
		return
	}
	var req RevokeRequest
	if !decodeJSON(w, r, &req, 4096) {
		return
	}
	if err := s.Store.RevokeCert(r.Context(), dev.CertFingerprint, "", req.Reason); err != nil {
		s.Log.Error("revoke failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	_ = s.Store.InsertEvent(r.Context(), dev.ID, "revoke", mustJSON(map[string]string{"reason": req.Reason}))
	s.Log.Warn("device certificate revoked", "device", dev.ID, "reason", req.Reason)
	w.WriteHeader(http.StatusNoContent)
}

// --- bundles ---

func (s *Server) handleListBundles(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListBundles(r.Context())
	if err != nil {
		s.Log.Error("bundle list failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := BundlesListResponse{Bundles: make([]BundleJSON, 0, len(list))}
	for i := range list {
		resp.Bundles = append(resp.Bundles, toBundleJSON(&list[i]))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRegisterBundle(w http.ResponseWriter, r *http.Request) {
	var req RegisterBundleRequest
	if !decodeJSON(w, r, &req, 4096) {
		return
	}
	if req.ObjectKey == "" {
		writeError(w, http.StatusBadRequest, "object_key required")
		return
	}
	size, err := s.Objects.StatObject(r.Context(), req.ObjectKey)
	if errors.Is(err, storage.ErrNoObject) {
		writeError(w, http.StatusBadRequest, "object not found in store")
		return
	}
	if err != nil {
		s.Log.Error("stat object failed", "key", req.ObjectKey, "err", err)
		writeError(w, http.StatusInternalServerError, "object store error")
		return
	}
	raw, err := s.Objects.GetObjectBytes(r.Context(), req.ObjectKey+".meta.json", 64<<10)
	if errors.Is(err, storage.ErrNoObject) {
		writeError(w, http.StatusBadRequest, "missing .meta.json sidecar")
		return
	}
	if err != nil {
		s.Log.Error("sidecar fetch failed", "key", req.ObjectKey, "err", err)
		writeError(w, http.StatusInternalServerError, "object store error")
		return
	}
	meta, err := bundles.ParseMeta(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if meta.SizeBytes != size {
		writeError(w, http.StatusBadRequest, "sidecar size_bytes does not match object size")
		return
	}
	b := &model.Bundle{
		ID:           uuid.NewString(),
		Version:      meta.Version,
		HardwareType: meta.HardwareType,
		ObjectKey:    req.ObjectKey,
		SHA256:       meta.SHA256,
		SizeBytes:    meta.SizeBytes,
		SignedBy:     meta.SignedBy,
	}
	if err := s.Store.InsertBundle(r.Context(), b); err != nil {
		if errors.Is(err, storage.ErrDuplicateBundle) {
			writeError(w, http.StatusConflict, "bundle already registered (version+hardware or key)")
			return
		}
		s.Log.Error("bundle insert failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	stored, err := s.Store.GetBundleByKey(r.Context(), req.ObjectKey)
	if err != nil || stored == nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, toBundleJSON(stored))
}

func (s *Server) handleScanBundles(w http.ResponseWriter, r *http.Request) {
	if s.Scanner == nil {
		writeError(w, http.StatusServiceUnavailable, "bundle scanning disabled")
		return
	}
	n, err := s.Scanner.RunOnce(r.Context())
	if err != nil {
		s.Log.Error("bundle scan failed", "err", err)
		writeError(w, http.StatusInternalServerError, "scan failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, ScanResponse{Registered: n})
}

func (s *Server) handlePatchBundle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusBadRequest, "malformed bundle id")
		return
	}
	var req PatchBundleRequest
	if !decodeJSON(w, r, &req, 4096) {
		return
	}
	if req.Status != "active" && req.Status != "deprecated" {
		writeError(w, http.StatusBadRequest, "status must be active or deprecated")
		return
	}
	b, err := s.Store.UpdateBundleStatus(r.Context(), id, req.Status)
	if err != nil {
		s.Log.Error("bundle update failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if b == nil {
		writeError(w, http.StatusNotFound, "no such bundle")
		return
	}
	writeJSON(w, http.StatusOK, toBundleJSON(b))
}

// --- rollouts ---

var rolloutTransitions = map[string][]string{
	"active":    {"draft", "paused"},
	"paused":    {"active"},
	"completed": {"active", "paused"},
	"aborted":   {"active", "paused"},
}

func (s *Server) handleListRollouts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListRollouts(r.Context())
	if err != nil {
		s.Log.Error("rollout list failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := RolloutsListResponse{Rollouts: make([]RolloutJSON, 0, len(list))}
	for i := range list {
		resp.Rollouts = append(resp.Rollouts, toRolloutJSON(&list[i]))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCreateRollout(w http.ResponseWriter, r *http.Request) {
	var req CreateRolloutRequest
	if !decodeJSON(w, r, &req, 4096) {
		return
	}
	if !model.Channels[req.Channel] {
		writeError(w, http.StatusBadRequest, "unknown channel")
		return
	}
	if req.Percentage < 0 || req.Percentage > 100 {
		writeError(w, http.StatusBadRequest, "percentage must be 0..100")
		return
	}
	if _, err := uuid.Parse(req.BundleID); err != nil {
		writeError(w, http.StatusBadRequest, "malformed bundle_id")
		return
	}
	b, err := s.Store.GetBundle(r.Context(), req.BundleID)
	if err != nil {
		s.Log.Error("bundle lookup failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if b == nil {
		writeError(w, http.StatusBadRequest, "no such bundle")
		return
	}
	created, err := s.Store.CreateRollout(r.Context(), &model.Rollout{
		ID:             uuid.NewString(),
		BundleID:       req.BundleID,
		Channel:        req.Channel,
		Percentage:     req.Percentage,
		AllowDowngrade: req.AllowDowngrade,
	})
	if err != nil || created == nil {
		s.Log.Error("rollout create failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, toRolloutJSON(created))
}

func (s *Server) handlePatchRollout(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusBadRequest, "malformed rollout id")
		return
	}
	var req PatchRolloutRequest
	if !decodeJSON(w, r, &req, 4096) {
		return
	}
	if req.Percentage == nil && req.Status == nil {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	current, err := s.Store.GetRollout(r.Context(), id)
	if err != nil {
		s.Log.Error("rollout lookup failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if current == nil {
		writeError(w, http.StatusNotFound, "no such rollout")
		return
	}

	result := current
	if req.Percentage != nil {
		if *req.Percentage < 0 || *req.Percentage > 100 {
			writeError(w, http.StatusBadRequest, "percentage must be 0..100")
			return
		}
		result, err = s.Store.UpdateRolloutPercentage(r.Context(), id, *req.Percentage)
		if err != nil {
			s.Log.Error("rollout percentage update failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if result == nil {
			writeError(w, http.StatusConflict,
				"percentage can only change while draft/active/paused")
			return
		}
	}
	if req.Status != nil {
		from, ok := rolloutTransitions[*req.Status]
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown target status")
			return
		}
		result, err = s.Store.TransitionRollout(r.Context(), id, *req.Status, from)
		if err != nil {
			s.Log.Error("rollout transition failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if result == nil {
			writeError(w, http.StatusConflict, "invalid status transition")
			return
		}
		s.Log.Info("rollout transition", "rollout", id, "to", *req.Status)
	}
	writeJSON(w, http.StatusOK, toRolloutJSON(result))
}

func (s *Server) handleRolloutStats(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusBadRequest, "malformed rollout id")
		return
	}
	ro, err := s.Store.GetRollout(r.Context(), id)
	if err != nil {
		s.Log.Error("rollout lookup failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if ro == nil {
		writeError(w, http.StatusNotFound, "no such rollout")
		return
	}
	stats, err := s.Store.RolloutStats(r.Context(), id)
	if err != nil {
		s.Log.Error("rollout stats failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, RolloutStatsResponse{
		Offered:      stats.Offered,
		InstalledOK:  stats.InstalledOK,
		Failed:       stats.Failed,
		FailRate1h:   stats.FailRate1h,
		PausedReason: ro.PausedReason,
	})
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
