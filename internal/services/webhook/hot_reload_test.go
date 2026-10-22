package webhook

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/configfile"
)

func TestSetURLsHotReload(t *testing.T) {
	got := make(chan struct{}, 2)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	svc := newWebhookService(t, &config.Config{})
	if svc.enabled() {
		t.Fatal("service should start disabled")
	}
	svc.SetURLs(target.URL)
	svc.SessionStarted(&bilibili.LiveRoomInfoDetail{RoomID: 1, Uname: "u", Title: "t"}, "sid", true, true)
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook after enabling URL")
	}

	svc.SetURLs("")
	svc.SessionStarted(&bilibili.LiveRoomInfoDetail{RoomID: 1, Uname: "u", Title: "t"}, "sid", true, true)
	select {
	case <-got:
		t.Fatal("unexpected webhook after clearing URL")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestBindConfigStoreAppliesURLChange(t *testing.T) {
	got := make(chan struct{}, 2)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	dir := t.TempDir()
	store, err := configfile.LoadStore(configfile.Options{
		ConfigPath:  dir + "/config.json",
		SecretsPath: dir + "/secrets.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := newWebhookService(t, &config.Config{})
	svc.BindConfigStore(store)
	if err := store.SetConfig(map[string]string{"WEBHOOK_URLS": target.URL}, nil); err != nil {
		t.Fatal(err)
	}
	if !svc.enabled() {
		t.Fatal("service should be enabled after config change")
	}
	svc.SessionStarted(&bilibili.LiveRoomInfoDetail{RoomID: 1, Uname: "u", Title: "t"}, "sid", true, true)
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook after config change")
	}
}
