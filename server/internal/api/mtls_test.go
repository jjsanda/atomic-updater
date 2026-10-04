package api

// Full-stack mTLS test: a real TLS listener with RequireAndVerifyClientCert,
// the production DeviceHandler, and real client certificates — the same
// classification path the e2e suite exercises with openssl-generated certs.

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMTLSEndToEnd(t *testing.T) {
	env := newTestEnv(t)

	ts := httptest.NewUnstartedServer(env.srv.DeviceHandler())
	pool := x509.NewCertPool()
	pool.AddCert(env.deviceCA)
	pool.AddCert(env.mfgCA)
	ts.TLS = &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  pool,
		MinVersion: tls.VersionTLS12,
	}
	ts.StartTLS()
	defer ts.Close()

	clientWith := func(cert tls.Certificate) *http.Client {
		tr := ts.Client().Transport.(*http.Transport).Clone()
		tr.TLSClientConfig.Certificates = []tls.Certificate{cert}
		return &http.Client{Transport: tr}
	}
	post := func(client *http.Client, path string, body any) (*http.Response, []byte) {
		t.Helper()
		raw, _ := json.Marshal(body)
		resp, err := client.Post(ts.URL+path, "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp, data
	}

	mfgCert, _ := makeTestLeaf(t, "mfg-fleet", env.mfgCA, env.mfgKey)
	mfgClient := clientWith(mfgCert)

	// 1. Enroll with the manufacturing certificate.
	csrPEM, deviceKey := makeTestCSR(t)
	resp, body := post(mfgClient, "/api/v1/enroll", EnrollRequest{
		CSRPEM: string(csrPEM), HardwareType: "x86_64-generic", Serial: "SIM-1",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("enroll over mTLS: %d: %s", resp.StatusCode, body)
	}
	var enrolled EnrollResponse
	if err := json.Unmarshal(body, &enrolled); err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(enrolled.CertificatePEM))
	if block == nil {
		t.Fatal("no cert in enroll response")
	}
	deviceCert := tls.Certificate{Certificate: [][]byte{block.Bytes}, PrivateKey: deviceKey}
	devClient := clientWith(deviceCert)

	// 2. Check in with the issued device certificate.
	resp, body = post(devClient, "/api/v1/checkin", CheckinRequest{
		Slot: "A", Version: "1.0.0", BootCount: 1, Healthy: true,
		LastUpdate: LastUpdate{Outcome: "none"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin over mTLS: %d: %s", resp.StatusCode, body)
	}
	var checkin CheckinResponse
	if err := json.Unmarshal(body, &checkin); err != nil {
		t.Fatal(err)
	}
	if checkin.Update != nil {
		t.Errorf("no rollout seeded, expected null update")
	}

	// 3. Report events with the device certificate.
	resp, body = post(devClient, "/api/v1/events", EventsRequest{
		Events: []DeviceEvent{{EventType: "download", Payload: json.RawMessage(`{"pct":100}`)}},
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("events over mTLS: %d: %s", resp.StatusCode, body)
	}

	// 4. Wrong-kind rejections.
	resp, _ = post(mfgClient, "/api/v1/checkin", CheckinRequest{
		Slot: "A", Version: "1.0.0", LastUpdate: LastUpdate{Outcome: "none"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("mfg cert on checkin: expected 403, got %d", resp.StatusCode)
	}
	resp, _ = post(devClient, "/api/v1/enroll", EnrollRequest{
		CSRPEM: string(csrPEM), HardwareType: "x86_64-generic",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("device cert on enroll: expected 403, got %d", resp.StatusCode)
	}

	// 5. A certificate from an unknown CA cannot even complete the handshake.
	rogueCA, rogueKey := makeTestCA(t, "Rogue CA")
	rogueCert, _ := makeTestLeaf(t, "rogue", rogueCA, rogueKey)
	rogueClient := clientWith(rogueCert)
	if _, err := rogueClient.Post(ts.URL+"/api/v1/checkin", "application/json",
		bytes.NewReader([]byte("{}"))); err == nil {
		t.Error("rogue client certificate should fail the TLS handshake")
	}
}
