package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLiveChat(t *testing.T) {
	var settings LiveChatSettings
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer lck_ws_1.secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid API key","code":"unauthorized"}`))
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/workspace":
			_, _ = w.Write([]byte(`{"id":"ws_1","name":"Shop","plan":"team"}`))
		case "POST /v1/settings":
			var up LiveChatSettingsUpdate
			_ = json.NewDecoder(r.Body).Decode(&up)
			if up.Origins != nil {
				settings.Origins = *up.Origins
			}
			if up.SSOOnly != nil {
				settings.SSOOnly = *up.SSOOnly
			}
			if up.EmbedOrigins != nil {
				t.Errorf("embed_origins sent though unset")
			}
			_ = json.NewEncoder(w).Encode(settings)
		case "POST /v1/identity-secret":
			_, _ = w.Write([]byte(`{"secret":"lcid_new"}`))
		case "POST /v1/webhooks/wh%2F1/secret", "POST /v1/webhooks/wh/1/secret":
			if r.URL.RawPath != "/v1/webhooks/wh%2F1/secret" {
				t.Errorf("id not escaped: %q", r.URL.RawPath)
			}
			_, _ = w.Write([]byte(`{"secret":"lcwh_new"}`))
		case "DELETE /v1/webhooks/wh_gone":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found","code":"not_found"}`))
		case "GET /v1/settings":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"forbidden","code":"admin_key_required"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	c := NewLiveChat(srv.URL+"/", "lck_ws_1.secret", "test")

	ws, err := c.Workspace(ctx)
	if err != nil || ws.ID != "ws_1" || ws.Plan != "team" {
		t.Fatalf("workspace %+v %v", ws, err)
	}
	origins, yes := []string{"https://a.example"}, true
	s, err := c.UpdateSettings(ctx, LiveChatSettingsUpdate{Origins: &origins, SSOOnly: &yes})
	if err != nil || !s.SSOOnly || len(s.Origins) != 1 {
		t.Fatalf("settings %+v %v", s, err)
	}
	if sec, err := c.RotateIdentitySecret(ctx); err != nil || sec != "lcid_new" {
		t.Fatalf("identity %q %v", sec, err)
	}
	if sec, err := c.RotateWebhookSecret(ctx, "wh/1"); err != nil || sec != "lcwh_new" {
		t.Fatalf("webhook secret %q %v", sec, err)
	}
	if err := c.DeleteWebhook(ctx, "wh_gone"); !IsNotFound(err) {
		t.Fatalf("delete missing: %v", err)
	}
	if _, err := c.Settings(ctx); err == nil || !strings.Contains(err.Error(), "admin scope") {
		t.Fatalf("non-admin key: %v", err)
	}
	if _, err := NewLiveChat(srv.URL, "lck_ws_1.wrong", "test").Workspace(ctx); err == nil || !strings.Contains(err.Error(), "check api_key") {
		t.Fatalf("bad key: %v", err)
	}
	if strings.Contains(c.String(), "secret") {
		t.Fatal("String leaks the key")
	}
}
