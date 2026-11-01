package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ConsolePrefix starts an operations console API token.
const ConsolePrefix = "hbc_"

// Console calls the operations console API (/admin/api) with a console API
// token (hbc_...). The console is behind Cloudflare Access, so it also
// sends an Access service token. A console token never sees or sets
// secrets.
type Console struct{ c *Client }

// NewConsole returns a console client for endpoint (DefaultEndpoint when
// empty). accessID and accessSecret are a Cloudflare Access service token;
// both empty sends none (a console without Access, such as a local one).
func NewConsole(endpoint, token, accessID, accessSecret, userAgent string) *Console {
	c := New(endpoint, token, userAgent)
	c.base = "/admin/api"
	c.unauthorized = "check the provider's console_token (made in the operations console under API tokens)"
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if accessID != "" || accessSecret != "" {
		c.headers = map[string]string{"CF-Access-Client-Id": accessID, "CF-Access-Client-Secret": accessSecret}
	}
	return &Console{c: c}
}

// CustomPlan is an account's Enterprise terms, set by an operator.
type CustomPlan struct {
	MaxApps           int    `json:"max_apps"`
	MaxConnections    int    `json:"max_connections"`
	DailyMessageLimit int64  `json:"daily_messages"`
	MonthlyPrice      int64  `json:"monthly_price"`
	Currency          string `json:"currency"`
	Note              string `json:"note,omitempty"`
}

// Account is a customer account as the console shows it.
type Account struct {
	ID            string      `json:"id"`
	Email         string      `json:"email"`
	Name          string      `json:"name"`
	Plan          string      `json:"plan"`
	EffectivePlan string      `json:"effective_plan"`
	Status        string      `json:"status"`
	Suspended     bool        `json:"suspended"`
	CustomPlan    *CustomPlan `json:"custom_plan,omitempty"`
	EmailVerified bool        `json:"email_verified"`
	Note          string      `json:"note,omitempty"`
	Tags          []string    `json:"tags"`
	ChatBeta      bool        `json:"chat_beta"`
}

type accountAnswer struct {
	Account Account `json:"account"`
}

// Account reads one account.
func (k *Console) Account(ctx context.Context, id string) (*Account, error) {
	var out accountAnswer
	if err := k.c.do(ctx, http.MethodGet, "/accounts/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out.Account, nil
}

// AccountByEmail finds the account with this email (case-insensitive); a
// 404 error when there is none.
func (k *Console) AccountByEmail(ctx context.Context, email string) (*Account, error) {
	cursor := ""
	for {
		var page struct {
			Items      []Account `json:"items"`
			NextCursor string    `json:"next_cursor"`
		}
		q := url.Values{"q": {email}, "limit": {"100"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		if err := k.c.do(ctx, http.MethodGet, "/accounts?"+q.Encode(), nil, &page); err != nil {
			return nil, err
		}
		for _, a := range page.Items {
			if strings.EqualFold(a.Email, email) {
				return &a, nil
			}
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			return nil, &Error{Status: http.StatusNotFound, Message: "no account has the email " + email}
		}
		cursor = page.NextCursor
	}
}

// SetCustomPlan sets the account's Enterprise terms; nil clears them.
func (k *Console) SetCustomPlan(ctx context.Context, id string, p *CustomPlan) (*Account, error) {
	var in any = map[string]bool{"clear": true}
	if p != nil {
		in = p
	}
	var out accountAnswer
	if err := k.c.do(ctx, http.MethodPost, "/accounts/"+url.PathEscape(id)+"/plan", in, &out); err != nil {
		return nil, err
	}
	return &out.Account, nil
}

// SetLabels replaces the account's operator note and tags.
func (k *Console) SetLabels(ctx context.Context, id, note string, tags []string) (*Account, error) {
	if tags == nil {
		tags = []string{}
	}
	var out accountAnswer
	err := k.c.do(ctx, http.MethodPut, "/accounts/"+url.PathEscape(id)+"/labels",
		map[string]any{"note": note, "tags": tags}, &out)
	if err != nil {
		return nil, err
	}
	return &out.Account, nil
}

// SetChatBeta lets the account into the Chat API beta, or out.
func (k *Console) SetChatBeta(ctx context.Context, id string, on bool) (*Account, error) {
	var out accountAnswer
	if err := k.c.do(ctx, http.MethodPut, "/accounts/"+url.PathEscape(id)+"/chat-beta", map[string]bool{"on": on}, &out); err != nil {
		return nil, err
	}
	return &out.Account, nil
}

// ConsoleApp is an app of any account as the console lists it. The
// console never returns its secret to a token.
type ConsoleApp struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Key            string     `json:"key"`
	Enabled        bool       `json:"enabled"`
	ClientEvents   bool       `json:"enable_client_events"`
	AllowedOrigins []string   `json:"allowed_origins"`
	AccountID      string     `json:"account_id"`
	Plan           string     `json:"plan"`
	CreatedAt      *time.Time `json:"created_at,omitempty"`
}

// Apps lists every app, or one account's when accountID is set.
func (k *Console) Apps(ctx context.Context, accountID string) ([]ConsoleApp, error) {
	var all []ConsoleApp
	cursor := ""
	for {
		var page struct {
			Items      []ConsoleApp `json:"items"`
			NextCursor string       `json:"next_cursor"`
		}
		q := url.Values{"limit": {"100"}}
		if accountID != "" {
			q.Set("account", accountID)
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		if err := k.c.do(ctx, http.MethodGet, "/apps?"+q.Encode(), nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == "" || page.NextCursor == cursor {
			return all, nil
		}
		cursor = page.NextCursor
	}
}
