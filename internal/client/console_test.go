package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleSendsTokenAndAccessHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CF-Access-Client-Id") != "id" || r.Header.Get("CF-Access-Client-Secret") != "secret" {
			http.Redirect(w, r, "https://hibira.cloudflareaccess.com/cdn-cgi/access/login", http.StatusFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer hbc_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/api/accounts":
			if r.URL.Query().Get("q") != "dev@hibira.io" {
				t.Errorf("q = %q", r.URL.Query().Get("q"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []Account{
				{ID: "acct_other", Email: "dev@hibira.io.example"}, {ID: "acct_dev", Email: "Dev@Hibira.io"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/admin/api/accounts/acct_dev/plan":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["clear"] != true {
				t.Errorf("plan body %v", in)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"account": Account{ID: "acct_dev"}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	k := NewConsole(srv.URL, "hbc_test", "id", "secret", "test")
	a, err := k.AccountByEmail(ctx, "dev@hibira.io")
	if err != nil || a.ID != "acct_dev" {
		t.Fatalf("AccountByEmail = %+v, %v", a, err)
	}
	if _, err := k.SetCustomPlan(ctx, "acct_dev", nil); err != nil {
		t.Fatal(err)
	}

	_, err = NewConsole(srv.URL, "hbc_test", "", "", "test").Account(ctx, "acct_dev")
	if err == nil || !strings.Contains(err.Error(), "Cloudflare Access") {
		t.Fatalf("without Access headers: %v", err)
	}
	_, err = NewConsole(srv.URL, "hbc_wrong", "id", "secret", "test").Account(ctx, "acct_dev")
	if err == nil || !strings.Contains(err.Error(), "console_token") {
		t.Fatalf("wrong token: %v", err)
	}
}
