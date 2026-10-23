package ops

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miku0139/linode-agent/internal/api"
)

func newWaitSvc(t *testing.T, status string) *Service {
	t.Helper()
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/account/events":
			fmt.Fprintf(w, `{"data":[{"id":9001,"action":"linode_reboot","status":%q,"created":%q,"entity":{"id":7,"type":"linode"}}],"page":1,"pages":1,"results":1}`, status, now)
		case r.Method == http.MethodPost && r.URL.Path == "/linode/instances/7/reboot":
			fmt.Fprint(w, `{}`)
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, `{"errors":[{"reason":"Not found"}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	c := api.New("t")
	c.BaseURL = srv.URL
	c.MaxRetries = 0
	return New(c)
}

func TestWaitForResultFinishes(t *testing.T) {
	svc := newWaitSvc(t, "finished")
	res, err := svc.RebootLinode(context.Background(), 7, false)
	if err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	ev, err := svc.WaitForResult(context.Background(), res, 10*time.Second)
	if err != nil {
		t.Fatalf("WaitForResult: %v", err)
	}
	if ev["status"] != "finished" {
		t.Fatalf("event = %v", ev)
	}
}

func TestWaitForResultReportsFailure(t *testing.T) {
	svc := newWaitSvc(t, "failed")
	res, err := svc.RebootLinode(context.Background(), 7, false)
	if err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	if _, err := svc.WaitForResult(context.Background(), res, 10*time.Second); err == nil {
		t.Fatal("expected failure to surface")
	}
}

func TestWaitTargetMapping(t *testing.T) {
	if id, action, ok := waitTarget("linode_reboot", "linode/42"); !ok || id != 42 || action != "linode_reboot" {
		t.Fatalf("mapping = %d %q %v", id, action, ok)
	}
	if _, _, ok := waitTarget("linode_update", "linode/42"); ok {
		t.Fatal("linode_update has no event")
	}
	if _, _, ok := waitTarget("image_capture", "disk/3"); ok {
		t.Fatal("image_capture is not pollable this way")
	}
	if _, _, ok := waitTarget("linode_reboot", "linode/notanint"); ok {
		t.Fatal("bad id must not map")
	}
}
