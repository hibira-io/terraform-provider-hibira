package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultLiveChatEndpoint is Hibira Live Chat, whose REST API is at /v1.
const DefaultLiveChatEndpoint = "https://chat.hibira.io"

// LiveChat calls the Hibira Live Chat REST API with a workspace API key
// (lck_<workspace>.<secret>, admin scope). It is separate from Client: Live
// Chat keys belong to one workspace, not to a Hibira account.
type LiveChat struct {
	endpoint  string
	key       string
	userAgent string
	http      *http.Client
}

// NewLiveChat returns a Live Chat client (DefaultLiveChatEndpoint when
// endpoint is empty).
func NewLiveChat(endpoint, key, userAgent string) *LiveChat {
	if endpoint == "" {
		endpoint = DefaultLiveChatEndpoint
	}
	return &LiveChat{endpoint: strings.TrimRight(endpoint, "/"), key: key, userAgent: userAgent,
		http: &http.Client{Timeout: 30 * time.Second}}
}

// do makes one request. Nothing is retried, on purpose: a repeated secret
// rotation would replace the secret the first one returned, and a 429 is
// better shown than hidden.
func (c *LiveChat) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+"/v1"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
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
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			e.Message += ": check api_key (a Live Chat API key, made in Live Chat's Settings › API & webhooks)"
		case e.Code == "admin_key_required":
			e.Message += ": the API key needs the admin scope"
		}
		return e
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.Unmarshal(b, out)
}

// LiveChatWorkspace is the workspace an API key belongs to.
type LiveChatWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Plan string `json:"plan"`
}

// Workspace returns the key's workspace.
func (c *LiveChat) Workspace(ctx context.Context) (*LiveChatWorkspace, error) {
	var out LiveChatWorkspace
	if err := c.do(ctx, http.MethodGet, "/workspace", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LiveChatSettings are the workspace settings the provider manages.
type LiveChatSettings struct {
	Origins              []string `json:"origins"`
	EmbedOrigins         []string `json:"embed_origins"`
	SSOOnly              bool     `json:"sso_only"`
	IdentityVerification bool     `json:"identity_verification"`
	SSO                  bool     `json:"sso"`
}

// LiveChatSettingsUpdate changes some settings (nil = unchanged).
type LiveChatSettingsUpdate struct {
	Origins      *[]string `json:"origins,omitempty"`
	EmbedOrigins *[]string `json:"embed_origins,omitempty"`
	SSOOnly      *bool     `json:"sso_only,omitempty"`
}

// Settings returns the workspace settings.
func (c *LiveChat) Settings(ctx context.Context) (*LiveChatSettings, error) {
	var out LiveChatSettings
	if err := c.do(ctx, http.MethodGet, "/settings", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSettings changes settings and returns them.
func (c *LiveChat) UpdateSettings(ctx context.Context, in LiveChatSettingsUpdate) (*LiveChatSettings, error) {
	var out LiveChatSettings
	if err := c.do(ctx, http.MethodPost, "/settings", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RotateIdentitySecret makes a new verified-visitor secret (lcid_...).
func (c *LiveChat) RotateIdentitySecret(ctx context.Context) (string, error) {
	var out struct {
		Secret string `json:"secret"`
	}
	err := c.do(ctx, http.MethodPost, "/identity-secret", nil, &out)
	return out.Secret, err
}

// RotateSSOSecret makes a new single sign-on secret (lcsso_...).
func (c *LiveChat) RotateSSOSecret(ctx context.Context) (string, error) {
	var out struct {
		Secret string `json:"secret"`
	}
	err := c.do(ctx, http.MethodPost, "/sso-secret", nil, &out)
	return out.Secret, err
}

// LiveChatWebhook is a webhook endpoint of the workspace. Secret is set
// only when it is made or rotated.
type LiveChatWebhook struct {
	ID       string   `json:"id"`
	URL      string   `json:"url"`
	Events   []string `json:"events"`
	Disabled bool     `json:"disabled"`
	Secret   string   `json:"secret,omitempty"`
}

func liveChatWebhookPath(id string) string { return "/webhooks/" + url.PathEscape(id) }

// Webhooks lists the workspace's endpoints.
func (c *LiveChat) Webhooks(ctx context.Context) ([]LiveChatWebhook, error) {
	var out struct {
		Data []LiveChatWebhook `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/webhooks", nil, &out)
	return out.Data, err
}

// Webhook returns one endpoint.
func (c *LiveChat) Webhook(ctx context.Context, id string) (*LiveChatWebhook, error) {
	var out LiveChatWebhook
	if err := c.do(ctx, http.MethodGet, liveChatWebhookPath(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateWebhook adds an endpoint; the answer carries its secret.
func (c *LiveChat) CreateWebhook(ctx context.Context, u string, events []string) (*LiveChatWebhook, error) {
	var out LiveChatWebhook
	if err := c.do(ctx, http.MethodPost, "/webhooks", map[string]any{"url": u, "events": events}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateWebhook changes an endpoint's URL and events and enables it.
func (c *LiveChat) UpdateWebhook(ctx context.Context, id, u string, events []string) (*LiveChatWebhook, error) {
	var out LiveChatWebhook
	if err := c.do(ctx, http.MethodPost, liveChatWebhookPath(id), map[string]any{"url": u, "events": events, "disabled": false}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RotateWebhookSecret makes a new signing secret for an endpoint.
func (c *LiveChat) RotateWebhookSecret(ctx context.Context, id string) (string, error) {
	var out struct {
		Secret string `json:"secret"`
	}
	err := c.do(ctx, http.MethodPost, liveChatWebhookPath(id)+"/secret", nil, &out)
	return out.Secret, err
}

// DeleteWebhook removes an endpoint.
func (c *LiveChat) DeleteWebhook(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, liveChatWebhookPath(id), nil, nil)
}

// String keeps the key out of logs and error dumps.
func (c *LiveChat) String() string { return fmt.Sprintf("LiveChat(%s)", c.endpoint) }
