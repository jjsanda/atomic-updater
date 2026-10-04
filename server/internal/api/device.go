package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"atomic-updater/server/internal/auth"
	"atomic-updater/server/internal/ca"
	"atomic-updater/server/internal/model"
	"atomic-updater/server/internal/rollout"
)

const (
	maxEnrollBody  = 1 << 20 // 1 MiB
	maxCheckinBody = 64 << 10
	maxEventsBody  = 2 << 20
	maxEventBatch  = 64
	maxEventBytes  = 16 << 10
)

// handleEnroll implements first-boot enrollment (design doc §11.3,
// docs/api.md §1.1). Peer is guaranteed manufacturing-kind by middleware.
func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	var req EnrollRequest
	if !decodeJSON(w, r, &req, maxEnrollBody) {
		return
	}
	if !model.HardwareTypes[req.HardwareType] {
		writeError(w, http.StatusBadRequest, "unknown hardware_type")
		return
	}
	csr, err := ca.ParseCSR([]byte(req.CSRPEM))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid CSR: "+err.Error())
		return
	}
	pubFP, err := auth.PubkeyFingerprint(csr.PublicKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported CSR key type")
		return
	}

	existing, err := s.Store.GetDeviceByPubkey(r.Context(), pubFP)
	if err != nil {
		s.Log.Error("enroll: pubkey lookup failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if existing != nil {
		// Idempotent re-enrollment: same key, fresh certificate — unless the
		// device's current cert was revoked, which makes the key untrusted.
		revoked, err := s.Store.IsRevoked(r.Context(), existing.CertFingerprint)
		if err != nil {
			s.Log.Error("enroll: revocation check failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if revoked {
			writeError(w, http.StatusForbidden, "device certificate revoked; contact operations")
			return
		}
		certPEM, cert, err := s.Issuer.Issue(csr, existing.ID)
		if err != nil {
			s.Log.Error("enroll: reissue failed", "err", err)
			writeError(w, http.StatusInternalServerError, "certificate issuance failed")
			return
		}
		if err := s.Store.UpdateDeviceCert(r.Context(), existing.ID, auth.CertFingerprint(cert)); err != nil {
			s.Log.Error("enroll: cert update failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		payload, _ := json.Marshal(map[string]any{"reenroll": true, "serial": req.Serial})
		_ = s.Store.InsertEvent(r.Context(), existing.ID, "enroll", payload)
		s.Log.Info("device re-enrolled", "device", existing.ID)
		writeJSON(w, http.StatusOK, EnrollResponse{
			DeviceID:       existing.ID,
			CertificatePEM: string(certPEM),
			CAChainPEM:     string(s.CAChainPEM),
		})
		return
	}

	id := uuid.NewString()
	certPEM, cert, err := s.Issuer.Issue(csr, id)
	if err != nil {
		s.Log.Error("enroll: issue failed", "err", err)
		writeError(w, http.StatusInternalServerError, "certificate issuance failed")
		return
	}
	dev := &model.Device{
		ID:                id,
		CertFingerprint:   auth.CertFingerprint(cert),
		PubkeyFingerprint: pubFP,
		HardwareType:      req.HardwareType,
		Serial:            req.Serial,
		Channel:           "dev",
	}
	if err := s.Store.CreateDevice(r.Context(), dev); err != nil {
		s.Log.Error("enroll: device create failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	payload, _ := json.Marshal(map[string]any{"hardware_type": req.HardwareType, "serial": req.Serial})
	_ = s.Store.InsertEvent(r.Context(), id, "enroll", payload)
	s.Log.Info("device enrolled", "device", id, "hw", req.HardwareType, "serial", req.Serial)
	writeJSON(w, http.StatusCreated, EnrollResponse{
		DeviceID:       id,
		CertificatePEM: string(certPEM),
		CAChainPEM:     string(s.CAChainPEM),
	})
}

// handleCheckin implements docs/api.md §1.2. Middleware guarantees a known,
// non-retired device in the context.
func (s *Server) handleCheckin(w http.ResponseWriter, r *http.Request) {
	dev, ok := DeviceFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "unknown device")
		return
	}
	var req CheckinRequest
	if !decodeJSON(w, r, &req, maxCheckinBody) {
		return
	}
	if req.Slot != "A" && req.Slot != "B" {
		writeError(w, http.StatusBadRequest, "slot must be A or B")
		return
	}
	version, err := rollout.NormalizeVersion(req.Version)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid version: "+err.Error())
		return
	}
	if !model.UpdateOutcomes[req.LastUpdate.Outcome] {
		writeError(w, http.StatusBadRequest, "invalid last_update.outcome")
		return
	}

	if err := s.Store.UpdateDeviceCheckin(r.Context(), dev.ID, req.Slot, version,
		req.BundleID, req.BootCount, req.Healthy, req.LastUpdate.Outcome); err != nil {
		s.Log.Error("checkin: update failed", "device", dev.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"slot": req.Slot, "version": version, "healthy": req.Healthy, "uptime_s": req.UptimeS,
	})
	_ = s.Store.InsertEvent(r.Context(), dev.ID, "checkin", payload)

	// Evaluate the offer against the state just reported.
	fresh := *dev
	fresh.CurrentSlot = req.Slot
	fresh.CurrentVersion = version
	fresh.CurrentBundleID = req.BundleID
	offer, err := s.Controller.Evaluate(r.Context(), &fresh)
	if err != nil {
		s.Log.Error("checkin: offer evaluation failed", "device", dev.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := CheckinResponse{}
	if offer != nil {
		resp.Update = &UpdateAssignment{
			RolloutID:      offer.RolloutID,
			BundleID:       offer.BundleID,
			Version:        offer.Version,
			URL:            offer.URL,
			SHA256:         offer.SHA256,
			SizeBytes:      offer.SizeBytes,
			AllowDowngrade: offer.AllowDowngrade,
		}
		s.Log.Info("update offered", "device", dev.ID, "version", offer.Version, "rollout", offer.RolloutID)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleEvents implements docs/api.md §1.3 including auto-pause evaluation.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	dev, ok := DeviceFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "unknown device")
		return
	}
	var req EventsRequest
	if !decodeJSON(w, r, &req, maxEventsBody) {
		return
	}
	if len(req.Events) == 0 {
		writeError(w, http.StatusBadRequest, "empty event batch")
		return
	}
	if len(req.Events) > maxEventBatch {
		writeError(w, http.StatusBadRequest, "too many events in batch (max 64)")
		return
	}
	for _, e := range req.Events {
		if !model.DeviceEventTypes[e.EventType] {
			writeError(w, http.StatusBadRequest, "unknown event_type "+e.EventType)
			return
		}
		if len(e.Payload) > maxEventBytes {
			writeError(w, http.StatusBadRequest, "event payload exceeds 16KiB")
			return
		}
	}

	accepted := 0
	for _, e := range req.Events {
		if err := s.Store.InsertEvent(r.Context(), dev.ID, e.EventType, e.Payload); err != nil {
			s.Log.Error("event insert failed", "device", dev.ID, "type", e.EventType, "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		accepted++

		switch e.EventType {
		case "install-ok":
			_ = s.Store.SetDeviceOutcome(r.Context(), dev.ID, "ok")
		case "install-fail":
			_ = s.Store.SetDeviceOutcome(r.Context(), dev.ID, "fail")
		case "rollback":
			_ = s.Store.SetDeviceOutcome(r.Context(), dev.ID, "rollback")
		}
		if e.EventType == "install-fail" || e.EventType == "rollback" {
			var ref struct {
				RolloutID string `json:"rollout_id"`
			}
			if err := json.Unmarshal(e.Payload, &ref); err == nil && ref.RolloutID != "" {
				paused, err := s.Store.EvaluateAutoPause(r.Context(), ref.RolloutID,
					s.PauseMinCohort, s.PauseFailPct)
				if err != nil {
					s.Log.Error("auto-pause evaluation failed", "rollout", ref.RolloutID, "err", err)
				} else if paused {
					s.Log.Warn("rollout auto-paused on failure spike", "rollout", ref.RolloutID,
						"trigger_device", dev.ID)
				}
			}
		}
	}
	writeJSON(w, http.StatusAccepted, EventsResponse{Accepted: accepted})
}
