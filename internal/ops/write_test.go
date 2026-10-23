package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/miku0139/linode-agent/internal/api"
)

type capture struct {
	method string
	path   string
	body   map[string]any
}

func newWriteSvc(t *testing.T, status int, resp string) (*Service, *int32, *capture) {
	t.Helper()
	var calls int32
	got := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		got.method = r.Method
		got.path = r.URL.Path
		got.body = nil
		if r.Body != nil {
			var m map[string]any
			if err := json.NewDecoder(r.Body).Decode(&m); err == nil {
				got.body = m
			}
		}
		w.WriteHeader(status)
		if resp != "" {
			fmt.Fprint(w, resp)
		}
	}))
	t.Cleanup(srv.Close)
	c := api.New("t")
	c.BaseURL = srv.URL
	c.MaxRetries = 0
	return New(c), &calls, got
}

func TestDryRunSendsNothing(t *testing.T) {
	svc, calls, _ := newWriteSvc(t, 200, `{}`)
	res, err := svc.RebootLinode(context.Background(), 42, true)
	if err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	if !res.DryRun || res.Action != "linode_reboot" || res.Target != "linode/42" {
		t.Fatalf("result = %+v", res)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Fatalf("dry-run hit the API %d times", *calls)
	}
}

func TestRebootHitsCorrectEndpoint(t *testing.T) {
	svc, calls, got := newWriteSvc(t, 200, `{}`)
	res, err := svc.RebootLinode(context.Background(), 42, false)
	if err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	if res.DryRun {
		t.Fatal("expected applied result")
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("calls = %d", *calls)
	}
	if got.method != http.MethodPost || got.path != "/linode/instances/42/reboot" {
		t.Fatalf("request = %s %s", got.method, got.path)
	}
}

func TestShutdownAndBootAndDelete(t *testing.T) {
	cases := []struct {
		name   string
		call   func(*Service, context.Context, int, bool) (*Result, error)
		method string
		path   string
	}{
		{"boot", func(s *Service, c context.Context, id int, d bool) (*Result, error) {
			return s.BootLinode(c, id, d)
		}, "POST", "/linode/instances/7/boot"},
		{"shutdown", func(s *Service, c context.Context, id int, d bool) (*Result, error) {
			return s.ShutdownLinode(c, id, d)
		}, "POST", "/linode/instances/7/shutdown"},
		{"delete", func(s *Service, c context.Context, id int, d bool) (*Result, error) {
			return s.DeleteLinode(c, id, d)
		}, "DELETE", "/linode/instances/7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, _, got := newWriteSvc(t, 200, `{}`)
			if _, err := c.call(svc, context.Background(), 7, false); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if got.method != c.method || got.path != c.path {
				t.Fatalf("got %s %s, want %s %s", got.method, got.path, c.method, c.path)
			}
		})
	}
}

func TestCreateRedactsRootPass(t *testing.T) {
	svc, _, got := newWriteSvc(t, 200, `{"id":123,"label":"web2"}`)
	res, err := svc.CreateLinode(context.Background(), CreateLinodeInput{
		Region: "jp-osa", Type: "g6-nanode-1", Image: "linode/ubuntu24.04",
		Label: "web2", RootPass: "s3cr3t-pass",
	}, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.body["root_pass"] != "s3cr3t-pass" {
		t.Fatalf("root_pass not sent: %v", got.body)
	}
	if _, leaked := res.Request["root_pass"]; leaked {
		t.Fatalf("root_pass leaked into auditable request: %v", res.Request)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected a warning about root_pass")
	}
}

func TestUpdateRequiresAField(t *testing.T) {
	svc, _, _ := newWriteSvc(t, 200, `{}`)
	if _, err := svc.UpdateLinode(context.Background(), 1, UpdateLinodeInput{}, true); err == nil {
		t.Fatal("expected error when nothing to change")
	}
	label := "renamed"
	res, err := svc.UpdateLinode(context.Background(), 1, UpdateLinodeInput{Label: &label}, true)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.Request["label"] != "renamed" {
		t.Fatalf("request = %v", res.Request)
	}
}

func TestResizeAndVolumeOps(t *testing.T) {
	svc, _, got := newWriteSvc(t, 200, `{}`)
	if _, err := svc.ResizeLinode(context.Background(), 5, ResizeLinodeInput{Type: "g6-standard-1"}, false); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if got.path != "/linode/instances/5/resize" || got.body["type"] != "g6-standard-1" {
		t.Fatalf("resize request = %s %v", got.path, got.body)
	}

	if _, err := svc.AttachVolume(context.Background(), 9, 5, false); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if got.path != "/volumes/9/attach" || got.body["linode_id"] != float64(5) {
		t.Fatalf("attach request = %s %v", got.path, got.body)
	}

	if _, err := svc.DetachVolume(context.Background(), 9, false); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if got.path != "/volumes/9/detach" {
		t.Fatalf("detach path = %s", got.path)
	}

	if _, err := svc.ResizeVolume(context.Background(), 9, 200, false); err != nil {
		t.Fatalf("ResizeVolume: %v", err)
	}
	if got.path != "/volumes/9/resize" || got.body["size"] != float64(200) {
		t.Fatalf("volume resize = %s %v", got.path, got.body)
	}

	if _, err := svc.DeleteVolume(context.Background(), 9, false); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
	if got.method != http.MethodDelete || got.path != "/volumes/9" {
		t.Fatalf("volume delete = %s %s", got.method, got.path)
	}
}

func TestCreateVolumeValidation(t *testing.T) {
	svc, _, _ := newWriteSvc(t, 200, `{}`)
	if _, err := svc.CreateVolume(context.Background(), CreateVolumeInput{Label: "x"}, true); err == nil {
		t.Fatal("expected size validation error")
	}
	if _, err := svc.CreateVolume(context.Background(), CreateVolumeInput{Label: "x", Size: 10}, true); err == nil {
		t.Fatal("expected region/linode_id validation error")
	}
}

func TestCaptureImageValidation(t *testing.T) {
	svc, _, _ := newWriteSvc(t, 200, `{}`)
	if _, err := svc.CaptureImage(context.Background(), CaptureImageInput{Label: "img"}, true); err == nil {
		t.Fatal("expected disk_id validation error")
	}
	res, err := svc.CaptureImage(context.Background(), CaptureImageInput{DiskID: 3, Label: "img"}, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Target != "disk/3" {
		t.Fatalf("target = %s", res.Target)
	}
}
