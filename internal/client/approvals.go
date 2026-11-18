package client

import (
	"context"
	"net/http"
	"net/url"
)

// ApprovalsMember is a person in an Approvals workspace.
type ApprovalsMember struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// ApproverGroup is a named set of members a request can be sent to.
type ApproverGroup struct {
	Slug    string   `json:"slug"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

// ApprovalsWorkspace is a Hibira Approvals workspace, as its members see it.
type ApprovalsWorkspace struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Plan    string            `json:"plan"`
	AppID   string            `json:"app_id"`
	Role    string            `json:"role"`
	You     string            `json:"you"`
	Members []ApprovalsMember `json:"members"`
	Groups  []ApproverGroup   `json:"groups"`
	// Rules and FollowUp are nil from servers before pusher-server#898.
	Rules    []ApprovalRule     `json:"rules"`
	FollowUp *ApprovalsFollowUp `json:"follow_up"`
}

// ApprovalRule auto-approves matching requests. Patterns match the whole
// text, ignoring case; * stands for any run of characters.
type ApprovalRule struct {
	ID     string   `json:"id,omitempty"`
	Name   string   `json:"name"`
	Agents []string `json:"agents,omitempty"`
	Titles []string `json:"titles,omitempty"`
	Except []string `json:"except,omitempty"`
	Off    bool     `json:"off,omitempty"`
}

// ApprovalsFollowUp is when a request without an answer is reminded and
// escalated (0: never).
type ApprovalsFollowUp struct {
	RemindMinutes   int      `json:"remind_minutes"`
	EscalateMinutes int      `json:"escalate_minutes"`
	EscalateTo      []string `json:"escalate_to,omitempty"`
}

// ApprovalsWebhook is an endpoint told about requests and decisions.
// Secret is set only when it is made or rotated.
type ApprovalsWebhook struct {
	ID       string   `json:"id"`
	URL      string   `json:"url"`
	Events   []string `json:"events"`
	Disabled bool     `json:"disabled"`
	Secret   string   `json:"secret,omitempty"`
}

func workspacePath(id string) string { return "/approvals/workspaces/" + url.PathEscape(id) }

// ApprovalsWorkspaces lists the workspaces the token's owner is in.
func (c *Client) ApprovalsWorkspaces(ctx context.Context) ([]ApprovalsWorkspace, error) {
	var out struct {
		Items []ApprovalsWorkspace `json:"items"`
	}
	err := c.do(ctx, http.MethodGet, "/approvals/workspaces", nil, &out)
	return out.Items, err
}

// ApprovalsWorkspace reads one workspace (404 when the token's owner is not
// in it).
func (c *Client) ApprovalsWorkspace(ctx context.Context, id string) (*ApprovalsWorkspace, error) {
	var out ApprovalsWorkspace
	if err := c.do(ctx, http.MethodGet, workspacePath(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateApprovalsWorkspace makes a workspace; the token's owner is its admin.
func (c *Client) CreateApprovalsWorkspace(ctx context.Context, name string) (*ApprovalsWorkspace, error) {
	var out ApprovalsWorkspace
	if err := c.do(ctx, http.MethodPost, "/approvals/workspaces", map[string]string{"name": name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RenameApprovalsWorkspace changes a workspace's name.
func (c *Client) RenameApprovalsWorkspace(ctx context.Context, id, name string) (*ApprovalsWorkspace, error) {
	var out ApprovalsWorkspace
	if err := c.do(ctx, http.MethodPatch, workspacePath(id), map[string]string{"name": name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetApprovalsMember adds a member or changes their role.
func (c *Client) SetApprovalsMember(ctx context.Context, id string, m ApprovalsMember) (*ApprovalsWorkspace, error) {
	var out ApprovalsWorkspace
	if err := c.do(ctx, http.MethodPut, workspacePath(id)+"/members", m, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveApprovalsMember takes a member out of the workspace.
func (c *Client) RemoveApprovalsMember(ctx context.Context, id, email string) error {
	return c.do(ctx, http.MethodDelete, workspacePath(id)+"/members/"+url.PathEscape(email), nil, nil)
}

// SetApproverGroups replaces the workspace's approver groups.
func (c *Client) SetApproverGroups(ctx context.Context, id string, groups []ApproverGroup) (*ApprovalsWorkspace, error) {
	if groups == nil {
		groups = []ApproverGroup{}
	}
	var out ApprovalsWorkspace
	if err := c.do(ctx, http.MethodPut, workspacePath(id)+"/groups", map[string]any{"groups": groups}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteApprovalsWorkspace deletes a workspace, confirmed with its name.
// It needs a token with delete access. Servers that cannot delete
// workspaces through the API answer 404 or 405.
func (c *Client) DeleteApprovalsWorkspace(ctx context.Context, id, name string) error {
	return c.do(ctx, http.MethodDelete, workspacePath(id)+"?confirm="+url.QueryEscape(name), nil, nil)
}

// SetApprovalRules replaces the workspace's auto-approve rules.
func (c *Client) SetApprovalRules(ctx context.Context, id string, rules []ApprovalRule) (*ApprovalsWorkspace, error) {
	if rules == nil {
		rules = []ApprovalRule{}
	}
	var out ApprovalsWorkspace
	if err := c.do(ctx, http.MethodPut, workspacePath(id)+"/rules", map[string]any{"rules": rules}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetApprovalsFollowUp sets the reminder and escalation.
func (c *Client) SetApprovalsFollowUp(ctx context.Context, id string, f ApprovalsFollowUp) (*ApprovalsWorkspace, error) {
	var out ApprovalsWorkspace
	if err := c.do(ctx, http.MethodPut, workspacePath(id)+"/follow-up", f, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func approvalsHookPath(ws, id string) string {
	return workspacePath(ws) + "/webhooks/" + url.PathEscape(id)
}

// ApprovalsWebhooks lists a workspace's endpoints.
func (c *Client) ApprovalsWebhooks(ctx context.Context, ws string) ([]ApprovalsWebhook, error) {
	var out struct {
		Webhooks []ApprovalsWebhook `json:"webhooks"`
	}
	err := c.do(ctx, http.MethodGet, workspacePath(ws)+"/webhooks", nil, &out)
	return out.Webhooks, err
}

// CreateApprovalsWebhook adds an endpoint; the answer carries its secret.
func (c *Client) CreateApprovalsWebhook(ctx context.Context, ws string, h ApprovalsWebhook) (*ApprovalsWebhook, error) {
	var out ApprovalsWebhook
	body := map[string]any{"url": h.URL, "events": h.Events, "disabled": h.Disabled}
	if err := c.do(ctx, http.MethodPost, workspacePath(ws)+"/webhooks", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateApprovalsWebhook changes an endpoint.
func (c *Client) UpdateApprovalsWebhook(ctx context.Context, ws string, h ApprovalsWebhook) (*ApprovalsWebhook, error) {
	var out ApprovalsWebhook
	body := map[string]any{"url": h.URL, "events": h.Events, "disabled": h.Disabled}
	if err := c.do(ctx, http.MethodPut, approvalsHookPath(ws, h.ID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RotateApprovalsWebhookSecret makes a new signing secret.
func (c *Client) RotateApprovalsWebhookSecret(ctx context.Context, ws, id string) (string, error) {
	var out struct {
		Secret string `json:"secret"`
	}
	err := c.do(ctx, http.MethodPost, approvalsHookPath(ws, id)+"/rotate", nil, &out)
	return out.Secret, err
}

// DeleteApprovalsWebhook removes an endpoint; it needs delete access.
func (c *Client) DeleteApprovalsWebhook(ctx context.Context, ws, id string) error {
	return c.do(ctx, http.MethodDelete, approvalsHookPath(ws, id), nil, nil)
}
