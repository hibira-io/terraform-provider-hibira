// Package client calls the Hibira management API (the customer API's app
// endpoints) with an API token.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is the Hibira site the API lives under.
const DefaultEndpoint = "https://hibira.io"

// Client is an authenticated Hibira API client.
type Client struct {
	endpoint  string
	token     string
	userAgent string
	http      *http.Client
}

// New returns a client for endpoint (DefaultEndpoint when empty).
func New(endpoint, token, userAgent string) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), token: token, userAgent: userAgent,
		http: &http.Client{Timeout: 30 * time.Second}}
}

// Error is an error answer from the API.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("hibira: %s (HTTP %d, %s)", e.Message, e.Status, e.Code)
	}
	return fmt.Sprintf("hibira: %s (HTTP %d)", e.Message, e.Status)
}

// IsNotFound reports whether err is a 404.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// IsConflict reports whether err is a version conflict (the app changed
// since it was read).
func IsConflict(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusConflict && e.Code == "conflict"
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+"/api/v1"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		e := &Error{Status: resp.StatusCode}
		var msg struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if json.Unmarshal(b, &msg) == nil && msg.Error != "" {
			e.Message, e.Code = msg.Error, msg.Code
		} else {
			e.Message = http.StatusText(resp.StatusCode)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			e.Message += ": check the provider's api_token (a Hibira API token, made on the portal's AI agents page)"
		}
		return e
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.Unmarshal(b, out)
}

// Webhook is one of an app's webhook endpoints.
type Webhook struct {
	URL    string   `json:"url"`
	Events []string `json:"events,omitempty"`
}

// Edge is an app's edge fan-out setting.
type Edge struct {
	Mode       string `json:"mode"`
	KeepDirect bool   `json:"keep_direct"`
}

// App is a Hibira app.
type App struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Key               string         `json:"key"`
	Secret            string         `json:"secret,omitempty"`
	Enabled           bool           `json:"enabled"`
	ClientEvents      bool           `json:"client_events"`
	SubscriptionCount bool           `json:"subscription_count"`
	AllowedOrigins    []string       `json:"allowed_origins"`
	Webhooks          []Webhook      `json:"webhooks"`
	Plan              string         `json:"plan"`
	Version           int64          `json:"version"`
	Cluster           string         `json:"cluster,omitempty"`
	Endpoint          map[string]any `json:"endpoint,omitempty"`
	Edge              *Edge          `json:"edge,omitempty"`
	History           bool           `json:"history"`
	APIAllowlist      []string       `json:"api_allowlist"`
}

// Host is where the app's clients connect ("" when the API didn't say).
func (a *App) Host() string {
	if h, ok := a.Endpoint["host"].(string); ok {
		return h
	}
	return ""
}

// AppCreate is a new app.
type AppCreate struct {
	Name           string   `json:"name"`
	AllowedOrigins []string `json:"allowed_origins"`
	ClientEvents   bool     `json:"client_events"`
	Cluster        string   `json:"cluster,omitempty"`
}

// AppUpdate changes the fields that are set.
type AppUpdate struct {
	Version           int64      `json:"version"`
	Name              *string    `json:"name,omitempty"`
	AllowedOrigins    *[]string  `json:"allowed_origins,omitempty"`
	ClientEvents      *bool      `json:"client_events,omitempty"`
	SubscriptionCount *bool      `json:"subscription_count,omitempty"`
	Enabled           *bool      `json:"enabled,omitempty"`
	Webhooks          *[]Webhook `json:"webhooks,omitempty"`
	EdgeMode          *string    `json:"edge_mode,omitempty"`
	EdgeKeepDirect    *bool      `json:"edge_keep_direct,omitempty"`
	History           *bool      `json:"history,omitempty"`
	APIAllowlist      *[]string  `json:"api_allowlist,omitempty"`
}

// Apps lists the apps the token can see.
func (c *Client) Apps(ctx context.Context) ([]App, error) {
	var out struct {
		Items []App `json:"items"`
	}
	err := c.do(ctx, http.MethodGet, "/apps", nil, &out)
	return out.Items, err
}

// App reads one app.
func (c *Client) App(ctx context.Context, id string) (*App, error) {
	var out App
	if err := c.do(ctx, http.MethodGet, "/apps/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateApp creates an app; the result's Secret is the only time the
// secret is returned.
func (c *Client) CreateApp(ctx context.Context, in AppCreate) (*App, error) {
	if in.AllowedOrigins == nil {
		in.AllowedOrigins = []string{}
	}
	var out App
	if err := c.do(ctx, http.MethodPost, "/apps", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// conflictRetries is how often ModifyApp rereads an app that changed
// between its read and its update.
const conflictRetries = 5

// ModifyApp reads the app, builds an update from it with f and applies it,
// retrying when someone else changed the app in between. f returns false
// when there is nothing to change.
func (c *Client) ModifyApp(ctx context.Context, id string, f func(*App) (AppUpdate, bool)) (*App, error) {
	for i := 0; ; i++ {
		app, err := c.App(ctx, id)
		if err != nil {
			return nil, err
		}
		up, ok := f(app)
		if !ok {
			return app, nil
		}
		up.Version = app.Version
		var out App
		err = c.do(ctx, http.MethodPatch, "/apps/"+url.PathEscape(id), up, &out)
		if err == nil {
			return &out, nil
		}
		if !IsConflict(err) || i >= conflictRetries {
			return nil, err
		}
	}
}

// DeleteApp deletes an app and its push instance.
func (c *Client) DeleteApp(ctx context.Context, id string) error {
	for i := 0; ; i++ {
		app, err := c.App(ctx, id)
		if err != nil {
			return err
		}
		err = c.do(ctx, http.MethodDelete, "/apps/"+url.PathEscape(id)+"?version="+strconv.FormatInt(app.Version, 10), nil, nil)
		if err == nil || !IsConflict(err) || i >= conflictRetries {
			return err
		}
	}
}

// RotateSecret gives the app a new secret; the old one keeps working for
// grace (0 = stops at once). The result carries the new secret.
func (c *Client) RotateSecret(ctx context.Context, id string, grace time.Duration) (*App, error) {
	for i := 0; ; i++ {
		app, err := c.App(ctx, id)
		if err != nil {
			return nil, err
		}
		var out App
		err = c.do(ctx, http.MethodPost, "/apps/"+url.PathEscape(id)+"/rotate-secret",
			map[string]any{"version": app.Version, "grace_minutes": int(grace / time.Minute)}, &out)
		if err == nil {
			return &out, nil
		}
		if !IsConflict(err) || i >= conflictRetries {
			return nil, err
		}
	}
}

// APNs is an app's APNs signing key, as the API shows it (without the
// private key).
type APNs struct {
	KeyID       string `json:"key_id"`
	TeamID      string `json:"team_id"`
	BundleID    string `json:"bundle_id"`
	Environment string `json:"environment"`
	PrivateKey  string `json:"private_key,omitempty"`
}

// FCM is an app's Firebase service account, as the API shows it.
type FCM struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
}

// Push is an app's push notification instance.
type Push struct {
	Enabled        bool   `json:"enabled"`
	InstanceID     string `json:"instance_id"`
	SecretKey      string `json:"secret_key"`
	VAPIDPublicKey string `json:"vapid_public_key"`
	Endpoint       string `json:"endpoint"`
	APNs           *APNs  `json:"apns"`
	FCM            *FCM   `json:"fcm"`
}

func pushPath(appID, sub string) string { return "/apps/" + url.PathEscape(appID) + "/push" + sub }

// Push reads an app's push settings.
func (c *Client) Push(ctx context.Context, appID string) (*Push, error) {
	var out Push
	if err := c.do(ctx, http.MethodGet, pushPath(appID, ""), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EnablePush turns push notifications on for an app.
func (c *Client) EnablePush(ctx context.Context, appID string) (*Push, error) {
	var out Push
	if err := c.do(ctx, http.MethodPost, pushPath(appID, ""), map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DisablePush turns push notifications off and deletes the instance.
func (c *Client) DisablePush(ctx context.Context, appID string) error {
	return c.do(ctx, http.MethodDelete, pushPath(appID, ""), nil, nil)
}

// SetAPNs sets (or with nil clears) the APNs key.
func (c *Client) SetAPNs(ctx context.Context, appID string, k *APNs) (*Push, error) {
	var out Push
	var err error
	if k == nil {
		err = c.do(ctx, http.MethodDelete, pushPath(appID, "/apns"), nil, &out)
	} else {
		err = c.do(ctx, http.MethodPut, pushPath(appID, "/apns"), k, &out)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// SetFCM sets (or with "" clears) the Firebase service account JSON.
func (c *Client) SetFCM(ctx context.Context, appID, serviceAccount string) (*Push, error) {
	var out Push
	var err error
	if serviceAccount == "" {
		err = c.do(ctx, http.MethodDelete, pushPath(appID, "/fcm"), nil, &out)
	} else {
		err = c.do(ctx, http.MethodPut, pushPath(appID, "/fcm"), map[string]string{"service_account": serviceAccount}, &out)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}
