package main

import (
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/pkg/detect"
)

func TestEgressGatewayFlagValidation(t *testing.T) {
	parse := func(args ...string) (*options, error) {
		o := &options{}
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		bindFlags(fs, o)
		return o, fs.Parse(args)
	}
	if o, err := parse(); err != nil || o.egressGatewayLogs != detect.GatewayLogsAuto {
		t.Fatalf("default = %q, %v; want auto", o.egressGatewayLogs, err)
	}
	for _, mode := range []string{"auto", "on", "off"} {
		if o, err := parse("--egress-gateway-logs=" + mode); err != nil || o.egressGatewayLogs != mode {
			t.Errorf("%s = %q, %v", mode, o.egressGatewayLogs, err)
		}
	}
	for _, bad := range []string{"maybe", "true", ""} {
		_, err := parse("--egress-gateway-logs=" + bad)
		if err == nil || !strings.Contains(err.Error(), "want auto, on or off") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

// TestGatewayLogAutoReadsNothing: auto and off build no reader, so no reconcile reads a gateway log; on builds
// one that reads nothing until a reconcile asks.
func TestGatewayLogAutoReadsNothing(t *testing.T) {
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer ts.Close()
	cfg := &rest.Config{Host: ts.URL}
	for _, mode := range []string{detect.GatewayLogsAuto, detect.GatewayLogsOff} {
		if g := gatewayLogs(mode, cfg); g != nil {
			t.Errorf("%s built a gateway log reader", mode)
		}
	}
	if g := gatewayLogs(detect.GatewayLogsOn, cfg); g == nil {
		t.Error("on built no gateway log reader")
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("GetLogs and List calls = %d, want 0", n)
	}
}
