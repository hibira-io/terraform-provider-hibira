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
	"strings"
	"time"
)

// DefaultLiveChatEndpoint is Hibira Live Chat's REST API on api.hibira.io,
// at /v1 under it.
const DefaultLiveChatEndpoint = "https://api.hibira.io/live-chat"

// LegacyLiveChatEndpoint is Live Chat's own host, the default before 0.9.0.
// Its REST API keeps answering, so state that holds it stays as it is.
const LegacyLiveChatEndpoint = "https://chat.hibira.io"

// LiveChat calls the Hibira Live Chat REST API with a workspace API key
// (lck_<workspace>.<secret>, admin scope), or with the provider's Hibira
// API token for a workspace named in the Hibira-Workspace header
// (Client.LiveChat), which the token's person must manage.
type LiveChat struct {
	endpoint  string
	key       string
	workspace string // set when key is a Hibira API token
	userAgent string
	http      *http.Client
}

// WorkspaceHeader names the workspace a Hibira API token acts on.
const WorkspaceHeader = "Hibira-Workspace"

// LiveChat returns a Live Chat client that uses this client's Hibira API
// token on workspace workspaceID (DefaultLiveChatEndpoint when endpoint is
// empty).
func (c *Client) LiveChat(endpoint, workspaceID string) *LiveChat {
	lc := NewLiveChat(endpoint, c.token, c.userAgent)
	lc.workspace = workspaceID
	return lc
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
	if c.workspace != "" {
		req.Header.Set(WorkspaceHeader, c.workspace)
	}
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
			Error     string                `json:"error"`
			Code      string                `json:"code"`
			Workspace *LiveChatWorkspaceRow `json:"workspace"`
		}
		if json.Unmarshal(b, &msg) == nil && msg.Error != "" {
			e.Message, e.Code, e.workspace = msg.Error, msg.Code, msg.Workspace
		} else {
			e.Message = http.StatusText(resp.StatusCode)
		}
		switch {
		case resp.StatusCode == http.StatusUnauthorized && c.workspace != "":
			e.Message += ": check the provider's api_token (a Hibira API token, made on the portal's API tokens page)"
		case resp.StatusCode == http.StatusUnauthorized:
			e.Message += ": check api_key (a Live Chat API key, made in Live Chat's Settings › API & webhooks)"
		case e.Code == "workspace_not_found":
			e.Message += " (workspace " + c.workspace + "): the token's account email must be an owner or admin there with the invite accepted (only the owner when single sign-on only or SCIM is on)"
		case e.Code == "delete_scope_required":
			e.Message += ": give the provider's api_token delete access (secrets are rotated and keys revoked)"
		case e.Code == "admin_key_required" && c.workspace != "":
			e.Message += ": the provider's api_token needs read and write access"
		case e.Code == "workspace_required":
			e.Message += ": set workspace_id"
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

// LiveChatWorkspaceRow is a workspace the token's account is in.
type LiveChatWorkspaceRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Plan     string `json:"plan"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
	API      bool   `json:"api"`
	// Usable is whether the token can act in the workspace (joined, and
	// the owner of an SSO-only or SCIM one); nil from servers before it.
	Usable *bool `json:"usable,omitempty"`
}

// CanUse is Usable, true when the server doesn't say.
func (w LiveChatWorkspaceRow) CanUse() bool { return w.Usable == nil || *w.Usable }

// CreateWorkspace opens a workspace named name that the Hibira API token's
// account owns (POST /v1/workspaces, a personal access token with write
// access). It starts on Free. When the account already owns one with that
// name the server answers 409 workspace_exists with it, returned here
// with existed true.
func (c *LiveChat) CreateWorkspace(ctx context.Context, name string) (*LiveChatWorkspaceRow, bool, error) {
	var out LiveChatWorkspaceRow
	err := c.do(ctx, http.MethodPost, "/workspaces", map[string]string{"name": name}, &out)
	var e *Error
	if errors.As(err, &e) && e.Status == http.StatusConflict && e.Code == "workspace_exists" && e.workspace != nil {
		return e.workspace, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &out, false, nil
}

// Workspaces lists the workspaces the Hibira API token's account is in
// (on a client made with an empty workspace).
func (c *LiveChat) Workspaces(ctx context.Context) ([]LiveChatWorkspaceRow, error) {
	var out struct {
		Data []LiveChatWorkspaceRow `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/workspaces", nil, &out)
	return out.Data, err
}

// LiveChatAPIKey is one of the workspace's API keys. Key (the secret) is
// set only when it is made.
type LiveChatAPIKey struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Scope string `json:"scope"`
	Hint  string `json:"hint"`
	Key   string `json:"key,omitempty"`
}

// APIKeys lists the workspace's API keys, without their secrets (Hibira
// API token only).
func (c *LiveChat) APIKeys(ctx context.Context) ([]LiveChatAPIKey, error) {
	var out struct {
		Data []LiveChatAPIKey `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/api-keys", nil, &out)
	return out.Data, err
}

// CreateAPIKey makes an API key; the answer carries its secret, once
// (Hibira API token only).
func (c *LiveChat) CreateAPIKey(ctx context.Context, name, scope string) (*LiveChatAPIKey, error) {
	var out LiveChatAPIKey
	if err := c.do(ctx, http.MethodPost, "/api-keys", map[string]string{"name": name, "scope": scope}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeAPIKey deletes an API key at once (Hibira API token only).
func (c *LiveChat) RevokeAPIKey(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api-keys/"+url.PathEscape(id), nil, nil)
}

// String keeps the key out of logs and error dumps.
func (c *LiveChat) String() string { return fmt.Sprintf("LiveChat(%s)", c.endpoint) }

// LiveChatAgent is a person in the workspace.
type LiveChatAgent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// Agents lists the workspace's agents.
func (c *LiveChat) Agents(ctx context.Context) ([]LiveChatAgent, error) {
	var out struct {
		Data []LiveChatAgent `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/agents", nil, &out)
	return out.Data, err
}

// LiveChatTeam is a team of agents conversations can be sent to.
type LiveChatTeam struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	AgentIDs []string `json:"agent_ids"`
}

func teamPath(id string) string { return "/teams/" + url.PathEscape(id) }

// Team returns one team.
func (c *LiveChat) Team(ctx context.Context, id string) (*LiveChatTeam, error) {
	var out LiveChatTeam
	if err := c.do(ctx, http.MethodGet, teamPath(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateTeam adds a team.
func (c *LiveChat) CreateTeam(ctx context.Context, name string, agentIDs []string) (*LiveChatTeam, error) {
	var out LiveChatTeam
	if err := c.do(ctx, http.MethodPost, "/teams", map[string]any{"name": name, "agent_ids": agentIDs}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateTeam changes a team's name and agents.
func (c *LiveChat) UpdateTeam(ctx context.Context, id, name string, agentIDs []string) (*LiveChatTeam, error) {
	var out LiveChatTeam
	if err := c.do(ctx, http.MethodPost, teamPath(id), map[string]any{"name": name, "agent_ids": agentIDs}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteTeam removes a team.
func (c *LiveChat) DeleteTeam(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, teamPath(id), nil, nil)
}

// DayHours is one weekday's opening hours (HH:MM, 24:00 for midnight).
type DayHours struct {
	Off  bool   `json:"off,omitempty"`
	From string `json:"from"`
	To   string `json:"to"`
}

// BusinessHours are the workspace's opening hours. Week starts on Sunday.
type BusinessHours struct {
	On       bool        `json:"on"`
	TimeZone string      `json:"time_zone"`
	Week     [7]DayHours `json:"week"`
	Closed   []string    `json:"closed"`
	Away     string      `json:"away,omitempty"`
}

// Hours returns the business hours.
func (c *LiveChat) Hours(ctx context.Context) (*BusinessHours, error) {
	var out BusinessHours
	if err := c.do(ctx, http.MethodGet, "/hours", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetHours replaces the business hours.
func (c *LiveChat) SetHours(ctx context.Context, h BusinessHours) (*BusinessHours, error) {
	var out BusinessHours
	if err := c.do(ctx, http.MethodPost, "/hours", h, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SavedReply is a canned reply agents insert with /shortcut.
type SavedReply struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Shortcut string `json:"shortcut,omitempty"`
	Text     string `json:"text"`
}

func savedReplyPath(id string) string { return "/saved-replies/" + url.PathEscape(id) }

// SavedReply returns one saved reply.
func (c *LiveChat) SavedReply(ctx context.Context, id string) (*SavedReply, error) {
	var out SavedReply
	if err := c.do(ctx, http.MethodGet, savedReplyPath(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSavedReply adds a saved reply.
func (c *LiveChat) CreateSavedReply(ctx context.Context, in SavedReply) (*SavedReply, error) {
	var out SavedReply
	body := map[string]string{"title": in.Title, "shortcut": in.Shortcut, "text": in.Text}
	if err := c.do(ctx, http.MethodPost, "/saved-replies", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSavedReply changes a saved reply.
func (c *LiveChat) UpdateSavedReply(ctx context.Context, in SavedReply) (*SavedReply, error) {
	var out SavedReply
	body := map[string]string{"title": in.Title, "shortcut": in.Shortcut, "text": in.Text}
	if err := c.do(ctx, http.MethodPost, savedReplyPath(in.ID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSavedReply removes a saved reply.
func (c *LiveChat) DeleteSavedReply(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, savedReplyPath(id), nil, nil)
}

// WebSource is a website the AI assistant's knowledge base reads.
type WebSource struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Found   string `json:"found,omitempty"`
	Pages   int    `json:"pages"`
	Entries int    `json:"entries"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

func webSourcePath(id string) string { return "/knowledge/sources/" + url.PathEscape(id) }

// WebSource returns one source.
func (c *LiveChat) WebSource(ctx context.Context, id string) (*WebSource, error) {
	var out WebSource
	if err := c.do(ctx, http.MethodGet, webSourcePath(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AddWebSource adds a website; Live Chat starts reading it.
func (c *LiveChat) AddWebSource(ctx context.Context, u string) (*WebSource, error) {
	var out WebSource
	if err := c.do(ctx, http.MethodPost, "/knowledge/sources", map[string]string{"url": u}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteWebSource removes a website and the entries read from it.
func (c *LiveChat) DeleteWebSource(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, webSourcePath(id), nil, nil)
}
