package doc

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func staticFiles() map[string][]byte {
	return map[string][]byte{
		"index.html":     []byte("<html>ok</html>"),
		"assets/app.js":  []byte("//js"),
		"assets/app.css": []byte("/*css*/"),
		"data.json":      []byte(`{"k":1}`),
		"icon.svg":       []byte("<svg/>"),
	}
}

func TestServeStaticOnListener_ServesIndexAndFiles(t *testing.T) {
	files := staticFiles()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- ServeStaticOnListener(ctx, files, ln) }()
	time.Sleep(50 * time.Millisecond)

	cases := []struct {
		path       string
		wantStatus int
		wantBody   string
		wantCType  string
	}{
		{"/", 200, "ok", "text/html; charset=utf-8"},
		{"/index.html", 200, "ok", "text/html; charset=utf-8"},
		{"/assets/app.js", 200, "//js", "text/javascript; charset=utf-8"},
		{"/assets/app.css", 200, "/*css*/", "text/css; charset=utf-8"},
		{"/data.json", 200, `{"k":1}`, "application/json"},
		{"/icon.svg", 200, "<svg/>", "image/svg+xml"},
		{"/missing", 404, "", ""},
	}
	for _, c := range cases {
		resp, err := http.Get("http://" + addr + c.path)
		if err != nil {
			t.Fatalf("GET %s: %v", c.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != c.wantStatus {
			t.Errorf("%s: status = %d, want %d", c.path, resp.StatusCode, c.wantStatus)
		}
		if c.wantBody != "" && !strings.Contains(string(body), c.wantBody) {
			t.Errorf("%s: body = %q, want contains %q", c.path, body, c.wantBody)
		}
		if c.wantCType != "" && resp.Header.Get("Content-Type") != c.wantCType {
			t.Errorf("%s: content-type = %q, want %q", c.path, resp.Header.Get("Content-Type"), c.wantCType)
		}
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("serve returned error: %v", err)
	}
}

func TestServeStaticOnListener_ClosedListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Close before serving so srv.Serve returns immediately, exercising the
	// errCh branch of the select.
	_ = ln.Close()

	if err := ServeStaticOnListener(context.Background(), staticFiles(), ln); err == nil {
		t.Error("expected error for closed listener")
	}
}

func TestServeStatic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() { errCh <- ServeStatic(ctx, staticFiles(), 0) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	if err := <-errCh; err != nil {
		t.Errorf("ServeStatic returned error: %v", err)
	}
}

func TestServeStatic_ListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	port := ln.Addr().(*net.TCPAddr).Port
	if err := ServeStatic(context.Background(), staticFiles(), port); err == nil {
		t.Error("expected listen error for in-use port")
	}
}

func TestWriteStaticExport(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"index.html":    []byte("<html>"),
		"assets/app.js": []byte("//js"),
	}
	if err := WriteStaticExport(files, dir); err != nil {
		t.Fatalf("WriteStaticExport: %v", err)
	}
	for p, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s: got %q, want %q", p, got, want)
		}
	}
}

func TestWriteStaticExport_MkdirError(t *testing.T) {
	dir := t.TempDir()
	// Place a file where a parent directory is needed, so MkdirAll fails.
	if err := os.WriteFile(filepath.Join(dir, "assets"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteStaticExport(map[string][]byte{"assets/app.js": []byte("//js")}, dir); err == nil {
		t.Error("expected MkdirAll error")
	}
}

func TestWriteStaticExport_WriteFileError(t *testing.T) {
	dir := t.TempDir()
	// Place a directory where the file is expected, so WriteFile fails.
	if err := os.MkdirAll(filepath.Join(dir, "index.html"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteStaticExport(map[string][]byte{"index.html": []byte("x")}, dir); err == nil {
		t.Error("expected WriteFile error")
	}
}
