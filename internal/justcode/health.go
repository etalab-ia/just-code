package justcode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HealthConfig tunes the backend health wait. Defaults mirror the shell: a
// 120-second wall-clock deadline and a 5-second per-request timeout.
type HealthConfig struct {
	// Deadline is the overall wall-clock bound. Zero disables the bound.
	Deadline time.Duration
	// PollInterval is the pause between probes.
	PollInterval time.Duration
	// RequestTimeout bounds each individual request, so a hung connection
	// cannot bypass the wall-clock deadline.
	RequestTimeout time.Duration
}

// DefaultHealthConfig returns the production health-wait settings.
func DefaultHealthConfig() HealthConfig {
	return HealthConfig{
		Deadline:       120 * time.Second,
		PollInterval:   500 * time.Millisecond,
		RequestTimeout: 5 * time.Second,
	}
}

// FetchBody performs a basic-authenticated GET to <endpoint><path> and returns
// the response body as a string. An empty password means "no auth".
func FetchBody(ctx context.Context, client *http.Client, endpoint, username, password, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(username, password)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// FetchJSON performs a basic-authenticated GET to <endpoint><path> and decodes
// the JSON response into v.
func FetchJSON(ctx context.Context, client *http.Client, endpoint, username, password, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(username, password)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s%s returned %s", endpoint, path, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

// WaitHealthy polls <endpoint>/global/health until the body contains "healthy"
// or the wall-clock deadline expires. It authenticates with basic auth.
func WaitHealthy(ctx context.Context, endpoint, username, password string, cfg HealthConfig, client *http.Client) error {
	if client == nil {
		client = &http.Client{Timeout: cfg.RequestTimeout}
	}
	var deadline time.Time
	if cfg.Deadline > 0 {
		deadline = time.Now().Add(cfg.Deadline)
	}
	for {
		healthy, err := probeHealthy(ctx, client, endpoint, username, password)
		if err == nil && healthy {
			return nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return fmt.Errorf("backend did not become healthy within %s", cfg.Deadline)
		}
		select {
		case <-time.After(cfg.PollInterval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// probeHealthy performs one health request. It returns false (with a nil
// error) when the body does not yet report healthy.
func probeHealthy(ctx context.Context, client *http.Client, endpoint, username, password string) (bool, error) {
	body, err := FetchBody(ctx, client, endpoint, username, password, "/global/health")
	if err != nil {
		return false, err
	}
	return strings.Contains(body, "healthy"), nil
}

type providerList struct {
	All []struct {
		ID string `json:"id"`
	} `json:"all"`
	Default map[string]any `json:"default"`
}

// CheckBackend reports the health response and Albert provider status of a
// backend endpoint, mirroring `just check`.
func CheckBackend(ctx context.Context, client *http.Client, endpoint, username, password string) error {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	body, err := FetchBody(ctx, client, endpoint, username, password, "/global/health")
	if err != nil {
		return err
	}
	fmt.Println(body)

	var pl providerList
	if err := FetchJSON(ctx, client, endpoint, username, password, "/provider", &pl); err != nil {
		return err
	}
	registered := false
	for _, p := range pl.All {
		if p.ID == "albert" {
			registered = true
			break
		}
	}
	if registered {
		fmt.Printf("albert provider: registered, default %v\n", pl.Default["albert"])
	} else {
		fmt.Println("albert provider: MISSING")
	}
	return nil
}
