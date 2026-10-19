/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evalsink

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeBindings(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bindings.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadBindings(t *testing.T) {
	path := writeBindings(t, `
- match: {unit: api.service}
  contract: ./contracts/api
  interfaces: {http: 8080}
  dependencies: {db: "db.internal:5432"}
- match: {container: 0123456789ab}
  contract: oci://ghcr.io/acme/web@sha256:abc
  host: 10.0.0.5
- match: {cgroup: /system.slice/x.service}
  contract: /abs/x
- match: {session: pacto-run-1}
  contract: ./s
- match: {pid: 42}
  contract: ./p
`)
	got, err := LoadBindings(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	want := []Target{
		{Name: "unit/api.service", Contract: filepath.Join(dir, "contracts/api"), Match: Selector{Unit: "api.service"}, Host: "127.0.0.1",
			Interfaces: map[string]int32{"http": 8080}, Dependencies: map[string]string{"db": "db.internal:5432"}},
		{Name: "container/0123456789ab", Contract: "oci://ghcr.io/acme/web@sha256:abc", Match: Selector{Container: "0123456789ab"}, Host: "10.0.0.5"},
		{Name: "cgroup/system.slice/x.service", Contract: "/abs/x", Match: Selector{Cgroup: "/system.slice/x.service"}, Host: "127.0.0.1"},
		{Name: "session/pacto-run-1", Contract: filepath.Join(dir, "s"), Match: Selector{Session: "pacto-run-1"}, Host: "127.0.0.1"},
		{Name: "pid/42", Contract: filepath.Join(dir, "p"), Match: Selector{PID: 42}, Host: "127.0.0.1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestLoadBindings_Errors(t *testing.T) {
	if _, err := LoadBindings(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("missing file accepted")
	}
	for name, body := range map[string]string{
		"unknown field":   "- match: {unit: a}\n  contract: ./a\n  bogus: 1\n",
		"no selector":     "- contract: ./a\n",
		"two selectors":   "- match: {unit: a, pid: 1}\n  contract: ./a\n",
		"no contract":     "- match: {unit: a}\n",
		"port range":      "- match: {unit: a}\n  contract: ./a\n  interfaces: {http: 70000}\n",
		"dep no port":     "- match: {unit: a}\n  contract: ./a\n  dependencies: {db: db.internal}\n",
		"dep port zero":   "- match: {unit: a}\n  contract: ./a\n  dependencies: {db: \"db:0\"}\n",
		"dep empty host":  "- match: {unit: a}\n  contract: ./a\n  dependencies: {db: \":5432\"}\n",
		"dep port is not": "- match: {unit: a}\n  contract: ./a\n  dependencies: {db: \"db:http\"}\n",
	} {
		if _, err := LoadBindings(writeBindings(t, body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// engine serves a fake Docker Engine API on a unix socket.
func engine(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

func TestDocker_Targets(t *testing.T) {
	started := time.Date(2026, 10, 18, 11, 0, 0, 0, time.UTC)
	containers := map[string]map[string]any{
		"web": {
			"Id": "web", "Name": "/web", "State": map[string]any{"StartedAt": started},
			"Config":          map[string]any{"Labels": map[string]string{LabelContract: "/c/web", LabelBindPrefix + "api": "8080", LabelDepPrefix + "db": "db:5432", "other": "x"}},
			"HostConfig":      map[string]any{"NetworkMode": "bridge"},
			"NetworkSettings": map[string]any{"Networks": map[string]any{"b": map[string]string{"IPAddress": "172.17.0.3"}, "a": map[string]string{"IPAddress": ""}}},
		},
		"host": {
			"Id": "host", "Name": "/host", "Config": map[string]any{"Labels": map[string]string{LabelContract: "/c/h"}},
			"HostConfig": map[string]any{"NetworkMode": "host"},
		},
		"badport": {"Id": "badport", "Name": "/badport", "Config": map[string]any{"Labels": map[string]string{LabelContract: "/c", LabelBindPrefix + "api": "http"}}},
		"baddep":  {"Id": "baddep", "Name": "/baddep", "Config": map[string]any{"Labels": map[string]string{LabelContract: "/c", LabelDepPrefix + "db": "nope"}}},
	}
	var filters string
	sock := engine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method %s", r.Method)
		}
		if r.URL.Path == "/containers/json" {
			filters = r.URL.Query().Get("filters")
			_ = json.NewEncoder(w).Encode([]map[string]string{{"Id": "web"}, {"Id": "host"}, {"Id": "badport"}, {"Id": "baddep"}, {"Id": "gone"}})
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/containers/"), "/json")
		c, ok := containers[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(c)
	}))
	d := NewDocker(sock)
	d.Labels = []string{"com.docker.compose.project=demo"}
	got, err := d.Targets(context.Background())
	if err == nil || !strings.Contains(err.Error(), "badport") || !strings.Contains(err.Error(), "baddep") || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
	// The extra labels are ANDed with the contract label by the Engine, which is what keeps an agent on a
	// shared daemon to one Compose project.
	if filters != `{"label":["`+LabelContract+`","com.docker.compose.project=demo"],"status":["running"]}` {
		t.Errorf("filters = %q", filters)
	}
	want := []Target{
		{Name: "container/web", Contract: "/c/web", Match: Selector{Container: "web"}, Start: started, Host: "172.17.0.3",
			Interfaces: map[string]int32{"api": 8080}, Dependencies: map[string]string{"db": "db:5432"}},
		{Name: "container/host", Contract: "/c/h", Match: Selector{Container: "host"}, Host: "127.0.0.1",
			Interfaces: map[string]int32{}, Dependencies: map[string]string{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestDocker_Errors(t *testing.T) {
	ctx := context.Background()
	if _, err := NewDocker(filepath.Join(t.TempDir(), "none.sock")).Targets(ctx); err == nil {
		t.Error("no engine: no error")
	}
	if _, err := (&Docker{Client: http.DefaultClient, Base: "http://bad host"}).Targets(ctx); err == nil {
		t.Error("bad base: no error")
	}
	sock := engine(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) }))
	if _, err := NewDocker(sock).Targets(ctx); err == nil || !strings.Contains(err.Error(), "/containers/json") {
		t.Errorf("bad json: %v", err)
	}
}
