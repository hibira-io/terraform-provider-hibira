package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestWaitingRooms(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(body))
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"rooms":[{"room":"presale","state":"open","admit_per_minute":300,"return_urls":["https://shop.example.com/"],"channel":"cache-waitroom-presale"}],"quota":{}}`))
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"room":"vip","state":"paused","admit_per_minute":10}`))
		case r.Method == http.MethodPatch:
			_, _ = w.Write([]byte(`{"room":"vip","state":"open","admit_per_minute":20}`))
		case r.Method == http.MethodDelete:
			_, _ = w.Write([]byte(`{"deleted":true}`))
		}
	}))
	defer srv.Close()
	c, ctx := New(srv.URL, "t", ""), context.Background()

	room, err := c.WaitingRoom(ctx, "app_1", "presale")
	if err != nil || room.AdmitPerMinute != 300 || room.Channel != "cache-waitroom-presale" || room.ReturnURLs[0] != "https://shop.example.com/" {
		t.Fatalf("room %+v err %v", room, err)
	}
	if _, err := c.WaitingRoom(ctx, "app_1", "nope"); !IsNotFound(err) {
		t.Fatalf("missing room: %v", err)
	}
	rate, state, empty := int64(10), "paused", ""
	if r, err := c.CreateWaitingRoom(ctx, "app_1", "vip", WaitingRoomInput{AdmitPerMinute: &rate, State: &state, StartsAt: &empty}); err != nil || r.State != "paused" {
		t.Fatalf("create %+v %v", r, err)
	}
	rate = 20
	if r, err := c.UpdateWaitingRoom(ctx, "app_1", "vip", WaitingRoomInput{AdmitPerMinute: &rate}); err != nil || r.AdmitPerMinute != 20 {
		t.Fatalf("update %+v %v", r, err)
	}
	if err := c.DeleteWaitingRoom(ctx, "app_1", "vip"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /api/v1/apps/app_1/waiting_rooms ",
		"GET /api/v1/apps/app_1/waiting_rooms ",
		`POST /api/v1/apps/app_1/waiting_rooms {"id":"vip","admit_per_minute":10,"state":"paused","starts_at":""}`,
		`PATCH /api/v1/apps/app_1/waiting_rooms/vip {"admit_per_minute":20}`,
		"DELETE /api/v1/apps/app_1/waiting_rooms/vip ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
