package cli

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func postTraces(addr string) (int, error) {
	resp, err := http.Post("http://"+addr+"/v1/traces", "application/json", strings.NewReader(`{"resourceSpans":[]}`))
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func TestDashboardOTLPReceiverFlag(t *testing.T) {
	t.Setenv("PACTO_DASHBOARD_OTLP_RECEIVER", "http://pacto-dashboard.pacto-system:4318")
	v := viper.New()
	v.SetEnvPrefix("PACTO")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()
	newDashboardCommand(app.NewService(nil, nil), v, "test", defaultDeps())
	if got := v.GetString("dashboard.otlp-receiver"); got != "http://pacto-dashboard.pacto-system:4318" {
		t.Fatalf("dashboard.otlp-receiver = %q", got)
	}

	isolateHost(t)
	dir := t.TempDir()
	writeBundle(t, dir, "svc")
	if _, err := runDashboard(t, app.NewService(nil, nil), viper.New(), dir, "--port", "0", "--otlp-receiver", "ftp://x:1"); err == nil || !strings.Contains(err.Error(), "is not http://HOST:PORT") {
		t.Fatalf("bad URL: %v", err)
	}
	if _, err := runDashboard(t, app.NewService(nil, nil), viper.New(), dir, "--port", "0", "--otlp-receiver", "http://127.0.0.1:0"); err != nil {
		t.Fatalf("loopback receiver: %v", err)
	}
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	if _, err := runDashboard(t, app.NewService(nil, nil), viper.New(), dir, "--port", "0", "--otlp-receiver", "http://"+busy.Addr().String()); err == nil || !strings.Contains(err.Error(), "--otlp-receiver:") {
		t.Fatalf("taken port: %v", err)
	}
	// Empty starts nothing.
	if fopts := dashboardFleetOptions(dir, nil, nil, nil, dashboardSources{}); fopts.LiveEdges.Receiver != nil {
		t.Fatal("a receiver with no flag")
	}

}

// http://127.0.0.1:0 serves on loopback until the context ends.
func TestDashboardReceiverServesOnLoopback(t *testing.T) {
	r, err := fleetsrc.NewReceiver("http://127.0.0.1:0", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	addr, err := serveReceiver(ctx, r)
	if err != nil || !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("serveReceiver = %q, %v", addr, err)
	}
	if code, err := postTraces(addr); err != nil || code != http.StatusOK {
		t.Fatalf("POST = %d, %v", code, err)
	}
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := postTraces(addr); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the receiver outlived its context")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDashboardReceiverIsNotOnTheDashboardMux(t *testing.T) {
	q := fleet.NewQuery(&fleet.FleetSnapshot{})
	o := dashboard.Options{UI: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}},
		Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = dashboard.NewServer(o).ServeOnListener(ctx, ln) }()
	var code int
	for range 100 {
		if code, err = postTraces(ln.Addr().String()); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code != http.StatusNotFound {
		t.Fatalf("POST /v1/traces on the dashboard = %d, %v", code, err)
	}
}
