// Package client implements the mTLS device-API client (docs/api.md §1).
package client

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client talks to atomic-server with a client certificate — either the
// manufacturing cert (enrollment only) or the issued device cert.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New builds an mTLS client. keyringPath is the pinned trust anchor for the
// SERVER certificate (the org root CA); certPath/keyPath are the CLIENT pair.
func New(baseURL, keyringPath, certPath, keyPath string, timeout time.Duration) (*Client, error) {
	keyring, err := os.ReadFile(keyringPath)
	if err != nil {
		return nil, fmt.Errorf("keyring: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(keyring) {
		return nil, fmt.Errorf("keyring %s: no certificates found", keyringPath)
	}
	clientCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("client keypair: %w", err)
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs:      roots,
					Certificates: []tls.Certificate{clientCert},
					MinVersion:   tls.VersionTLS12,
				},
			},
		},
	}, nil
}

// APIError carries the HTTP status so callers can react to 403/410
// (revoked / retired) differently from transient failures.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("server returned %d: %s", e.Status, e.Message)
}

func (c *Client) post(path string, req, resp any, want ...int) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpResp, err := c.HTTP.Post(c.BaseURL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return err
	}
	ok := false
	for _, s := range want {
		if httpResp.StatusCode == s {
			ok = true
		}
	}
	if !ok {
		var apiErr struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &apiErr)
		if apiErr.Error == "" {
			apiErr.Error = strings.TrimSpace(string(data))
		}
		return &APIError{Status: httpResp.StatusCode, Message: apiErr.Error}
	}
	if resp != nil {
		if err := json.Unmarshal(data, resp); err != nil {
			return fmt.Errorf("malformed response from %s: %w", path, err)
		}
	}
	return nil
}

func (c *Client) Enroll(req EnrollRequest) (*EnrollResponse, error) {
	var resp EnrollResponse
	if err := c.post("/api/v1/enroll", req, &resp, http.StatusCreated, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Checkin(req CheckinRequest) (*CheckinResponse, error) {
	var resp CheckinResponse
	if err := c.post("/api/v1/checkin", req, &resp, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Events(events []DeviceEvent) error {
	return c.post("/api/v1/events", EventsRequest{Events: events}, nil, http.StatusAccepted)
}
