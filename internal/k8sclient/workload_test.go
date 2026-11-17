package k8sclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/workloadshape"
)

func TestWorkloadGVRsCoverKinds(t *testing.T) {
	if len(WorkloadGVRs) != len(workloadshape.WorkloadKinds) {
		t.Errorf("WorkloadGVRs = %v, want one per kind %v", WorkloadGVRs, workloadshape.WorkloadKinds)
	}
	for _, k := range workloadshape.WorkloadKinds {
		if WorkloadGVRs[k].Resource != strings.ToLower(k)+"s" {
			t.Errorf("GVR of %s = %v", k, WorkloadGVRs[k])
		}
	}
}

// TestListPagePaginates drives ListPage against an API server that pages:
// the fake dynamic client drops Limit and Continue, so it cannot show them.
func TestListPagePaginates(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		asked = append(asked, r.URL.Path+"?limit="+q.Get("limit")+"&continue="+q.Get("continue"))
		if q.Get("continue") == "boom" {
			http.Error(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403}`, http.StatusForbidden)
			return
		}
		next := ""
		if q.Get("continue") == "" {
			next = "page-2"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"apiVersion":"apps/v1","kind":"DeploymentList","metadata":{"continue":%q},"items":[{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"d%s"}}]}`, next, q.Get("continue"))
	}))
	defer srv.Close()
	dyn, err := dynamic.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	c := &k8sGoClient{dynamic: dyn}
	gvr := WorkloadGVRs["Deployment"]
	data, next, err := c.ListPage(context.Background(), gvr, "shop", "")
	if err != nil || next != "page-2" || !strings.Contains(string(data), `"name":"d"`) {
		t.Fatalf("page 1 = %s, %q, %v", data, next, err)
	}
	data, next, err = c.ListPage(context.Background(), gvr, "", next)
	if err != nil || next != "" || !strings.Contains(string(data), `"name":"dpage-2"`) {
		t.Fatalf("page 2 = %s, %q, %v", data, next, err)
	}
	want := "[/apis/apps/v1/namespaces/shop/deployments?limit=500&continue= /apis/apps/v1/deployments?limit=500&continue=page-2]"
	if fmt.Sprint(asked) != want {
		t.Errorf("asked = %v\nwant    %s", asked, want)
	}
	if _, _, err := c.ListPage(context.Background(), gvr, "", "boom"); err == nil {
		t.Error("a failed list returned no error")
	}
}
