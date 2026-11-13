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

// DeleteApprovalsWorkspace deletes a workspace. Servers that cannot delete
// workspaces through the API answer 404 or 405.
func (c *Client) DeleteApprovalsWorkspace(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, workspacePath(id), nil, nil)
}
