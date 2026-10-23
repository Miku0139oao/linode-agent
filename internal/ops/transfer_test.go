package ops

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Miku0139oao/linode-agent/internal/api"
)

func newTransferSvc(t *testing.T) *Service {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/account/transfer", func(w http.ResponseWriter, r *http.Request) {
		// account endpoint: every field in GB
		fmt.Fprint(w, `{"billable":0,"quota":3000,"used":40,"region_transfers":[]}`)
	})
	mux.HandleFunc("/linode/instances", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[
			{"id":1,"label":"web1","region":"jp-osa"},
			{"id":2,"label":"db1","region":"ap-south"}
		],"page":1,"pages":1,"results":2}`)
	})
	mux.HandleFunc("/linode/instances/1/transfer", func(w http.ResponseWriter, r *http.Request) {
		// instance endpoint: used in BYTES, quota/billable in GB
		fmt.Fprint(w, `{"billable":0,"quota":1000,"used":10615768216}`)
	})
	mux.HandleFunc("/linode/instances/2/transfer", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"billable":0,"quota":1000,"used":22600994726}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := api.New("t")
	c.BaseURL = srv.URL
	c.MaxRetries = 0
	return New(c)
}

func TestNetworkUsageUnits(t *testing.T) {
	svc := newTransferSvc(t)
	rep, err := svc.NetworkUsage(context.Background())
	if err != nil {
		t.Fatalf("NetworkUsage: %v", err)
	}

	// Account fields are already GB and must not be divided again.
	if rep.PoolUsedGB != 40 || rep.PoolQuotaGB != 3000 || rep.PoolBillableGB != 0 {
		t.Fatalf("pool = %+v", rep)
	}
	if rep.Percent < 1.33 || rep.Percent > 1.34 {
		t.Fatalf("percent = %v", rep.Percent)
	}
	if len(rep.Instances) != 2 {
		t.Fatalf("instances = %d", len(rep.Instances))
	}

	byLabel := map[string]UsageRow{}
	for _, r := range rep.Instances {
		byLabel[r.Label] = r
	}
	web := byLabel["web1"]
	if web.UsedGB < 10.6 || web.UsedGB > 10.7 {
		t.Fatalf("web1 used = %v GB (instance endpoint is bytes)", web.UsedGB)
	}
	if web.QuotaGB != 1000 {
		t.Fatalf("web1 quota = %v GB, want 1000 (quota is already GB)", web.QuotaGB)
	}
	db := byLabel["db1"]
	if db.UsedGB < 22.5 || db.UsedGB > 22.7 {
		t.Fatalf("db1 used = %v GB", db.UsedGB)
	}
}

func TestBytesToGB(t *testing.T) {
	if got := BytesToGB(float64(1e9)); got != 1 {
		t.Fatalf("BytesToGB(1e9) = %v", got)
	}
	if got := BytesToGB(nil); got != 0 {
		t.Fatalf("BytesToGB(nil) = %v", got)
	}
}
