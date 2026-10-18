/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/evidence"
)

func TestClassifyProtocol(t *testing.T) {
	settings := []byte{0, 0, 6, 0x4, 0, 0, 0, 0, 0}
	tests := []struct {
		name string
		in   []byte
		want evidence.Protocol
	}{
		{"no bytes", nil, ""},
		{"settings frame", settings, evidence.ProtocolHTTP2},
		{"frame on a stream is not the server preface", []byte{0, 0, 0, 0x4, 0, 0, 0, 0, 1}, evidence.ProtocolOther},
		{"http1 status line", []byte("HTTP/1.1 4"), evidence.ProtocolHTTP1},
		{"tls alert", []byte{0x15, 3, 1, 0, 2, 2, 0x28}, evidence.ProtocolTLS},
		{"tls handshake", []byte{0x16, 3, 3}, evidence.ProtocolTLS},
		{"anything else", []byte("SSH-2.0-"), evidence.ProtocolOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyProtocol(tt.in); got != tt.want {
				t.Errorf("classifyProtocol(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// serveOnce accepts one connection, reads the client hello and answers with reply.
func serveOnce(t *testing.T, reply []byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, len(h2cHello))
		_, _ = conn.Read(buf)
		_, _ = conn.Write(reply)
	}()
	return ln.Addr().String()
}

func TestProbeProtocol(t *testing.T) {
	p := New(2 * time.Second)

	if got := p.ProbeProtocol(context.Background(), serveOnce(t, []byte{0, 0, 0, 0x4, 0, 0, 0, 0, 0})); got != evidence.ProtocolHTTP2 {
		t.Errorf("h2c server: got %q, want http2", got)
	}

	h1 := httptest.NewServer(http.NotFoundHandler())
	defer h1.Close()
	if got := p.ProbeProtocol(context.Background(), h1.Listener.Addr().String()); got != evidence.ProtocolHTTP1 {
		t.Errorf("HTTP/1 server: got %q, want http1", got)
	}

	// A TLS server answers the cleartext preface with an alert record (Go's crypto/tls just hangs up, which
	// reads as no answer: unverified, never a mismatch).
	if got := p.ProbeProtocol(context.Background(), serveOnce(t, []byte{0x15, 3, 1, 0, 2, 2, 0x32})); got != evidence.ProtocolTLS {
		t.Errorf("TLS server: got %q, want tls", got)
	}

	if got := p.ProbeProtocol(context.Background(), serveOnce(t, nil)); got != "" {
		t.Errorf("silent server: got %q, want empty", got)
	}

	// Nothing listens on a closed port: no answer, never an error.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	if got := p.ProbeProtocol(context.Background(), addr); got != "" {
		t.Errorf("closed port: got %q, want empty", got)
	}
}
