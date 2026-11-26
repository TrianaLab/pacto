package cli

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// fakeKubeAPI serves the Kubernetes API reads the k8s fleet sources make:
// discovery of the Pacto group, the given Pacto list and workload lists by
// resource (deployments, statefulsets, daemonsets, cronjobs; an absent one is
// empty). Every other path is not found, so the cluster identity is unread
// and the sources keep the context name. It points KUBECONFIG at the server.
func fakeKubeAPI(t *testing.T, pactos string, workloads map[string]string) {
	t.Helper()
	body := map[string]string{
		"/api":                               `{"kind":"APIVersions","versions":["v1"]}`,
		"/apis":                              `{"kind":"APIGroupList","apiVersion":"v1","groups":[{"name":"pacto.trianalab.io","versions":[{"groupVersion":"pacto.trianalab.io/v1","version":"v1"}],"preferredVersion":{"groupVersion":"pacto.trianalab.io/v1","version":"v1"}}]}`,
		"/api/v1":                            `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"v1","resources":[]}`,
		"/apis/pacto.trianalab.io/v1":        `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"pacto.trianalab.io/v1","resources":[{"name":"pactos","singularName":"pacto","namespaced":true,"kind":"Pacto","verbs":["list"]}]}`,
		"/apis/pacto.trianalab.io/v1/pactos": `{"apiVersion":"pacto.trianalab.io/v1","kind":"PactoList","metadata":{},"items":[` + pactos + `]}`,
	}
	for _, r := range []struct{ gv, resource string }{{"apps/v1", "deployments"}, {"apps/v1", "statefulsets"}, {"apps/v1", "daemonsets"}, {"batch/v1", "cronjobs"}} {
		body["/apis/"+r.gv+"/"+r.resource] = `{"apiVersion":"` + r.gv + `","kind":"List","metadata":{},"items":[` + workloads[r.resource] + `]}`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		b, ok := body[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			b = `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`
		}
		_, _ = w.Write([]byte(b))
	}))
	t.Cleanup(srv.Close)
	cfg := filepath.Join(t.TempDir(), "kubeconfig")
	mustWriteFile(t, cfg, "apiVersion: v1\nkind: Config\ncurrent-context: fake\n"+
		"clusters:\n- name: fake\n  cluster:\n    server: "+srv.URL+"\n"+
		"contexts:\n- name: fake\n  context:\n    cluster: fake\n    user: fake\n"+
		"users:\n- name: fake\n  user: {}\n")
	t.Setenv("KUBECONFIG", cfg)
}
