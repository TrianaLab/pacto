package doc

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestServe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, "# Test", "test-title", 0)
	}()

	// Give it a moment to start, then cancel
	time.Sleep(50 * time.Millisecond)
	cancel()

	if err := <-errCh; err != nil {
		t.Errorf("Serve returned error: %v", err)
	}
}

func TestServe_ListenError(t *testing.T) {
	// Bind to a port, then try to Serve on the same port to trigger listen error.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	port := ln.Addr().(*net.TCPAddr).Port
	err = Serve(context.Background(), "# Test", "test", port)
	if err == nil {
		t.Error("expected error when port is already in use")
	}
}

func TestServeOnListener_ClosedListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Close the listener before ServeOnListener uses it — this forces
	// srv.Serve to return immediately with an error, exercising the
	// errCh branch of the select in ServeOnListener.
	_ = ln.Close()

	err = ServeOnListener(context.Background(), "# Test", "test", ln)
	if err == nil {
		t.Error("expected error for closed listener")
	}
}

func TestServeOnListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeOnListener(ctx, "# Hello\nworld", "test-svc", ln)
	}()

	// Give the server a moment to start serving.
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/", addr))
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	html := string(body)

	if !strings.Contains(html, "<title>test-svc</title>") {
		t.Error("expected title in HTML")
	}
	if !strings.Contains(html, "# Hello") {
		t.Error("expected markdown content in HTML")
	}
	if !strings.Contains(html, "marked.parse") {
		t.Error("expected marked.js script in HTML")
	}

	cancel()

	if err := <-errCh; err != nil {
		t.Errorf("serve returned error: %v", err)
	}
}

func TestServeOnListener_HTMLEscaping(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeOnListener(ctx, "<script>alert('xss')</script>", "ti<tle", ln)
	}()

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/", addr))
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	html := string(body)

	if strings.Contains(html, "<script>alert") {
		t.Error("markdown content was not HTML-escaped")
	}
	if strings.Contains(html, "ti<tle") {
		t.Error("title was not HTML-escaped")
	}

	cancel()
	<-errCh
}
