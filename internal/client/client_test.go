package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestModifyAppRetriesOnConflict(t *testing.T) {
	var patches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hkb_pat_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			// Someone else bumps the version after our first read.
			_ = json.NewEncoder(w).Encode(App{ID: "app_1", Name: "old", Version: int64(1 + patches.Load())})
		case http.MethodPatch:
			var up AppUpdate
			_ = json.NewDecoder(r.Body).Decode(&up)
			if patches.Add(1) == 1 {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"changed","code":"conflict"}`))
				return
			}
			if up.Version != 2 || up.Name == nil || *up.Name != "new" {
				t.Errorf("update %+v", up)
			}
			_ = json.NewEncoder(w).Encode(App{ID: "app_1", Name: *up.Name, Version: 3})
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "hkb_pat_test", "test")
	app, err := c.ModifyApp(context.Background(), "app_1", func(a *App) (AppUpdate, bool) {
		n := "new"
		return AppUpdate{Name: &n}, a.Name != n
	})
	if err != nil || app.Name != "new" || patches.Load() != 2 {
		t.Fatalf("app %+v err %v patches %d", app, err, patches.Load())
	}
}

func TestErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Not found.","code":"not_found"}`))
	}))
	defer srv.Close()
	_, err := New(srv.URL, "t", "").App(context.Background(), "nope")
	if !IsNotFound(err) || IsConflict(err) {
		t.Fatalf("err %v", err)
	}
	if _, err := New(srv.URL+"/", "bad", "").Apps(context.Background()); err == nil || err.Error() != "hibira: Not found. (HTTP 404, not_found)" {
		t.Fatalf("err %v", err)
	}
}
