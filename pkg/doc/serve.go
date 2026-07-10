package doc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ServeStatic starts a local HTTP server that serves the given in-memory static
// export tree (path -> bytes, as produced by BuildStaticExport). It blocks until
// the context is cancelled.
func ServeStatic(ctx context.Context, files map[string][]byte, port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return ServeStaticOnListener(ctx, files, ln)
}

// ServeStaticOnListener is like ServeStatic but accepts an existing net.Listener.
// This is useful in tests where port 0 is used to obtain a random port and the
// caller needs the address before blocking. Requests for "/" resolve to
// index.html; unknown paths return 404.
func ServeStaticOnListener(ctx context.Context, files map[string][]byte, ln net.Listener) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		data, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType(name))
		_, _ = w.Write(data)
	})

	srv := &http.Server{Handler: mux}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		return srv.Close()
	case err := <-errCh:
		return err
	}
}

// WriteStaticExport writes the in-memory static export tree to dir, creating
// parent directories as needed.
func WriteStaticExport(files map[string][]byte, dir string) error {
	for p, data := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// contentType returns the MIME type for a static export entry based on its
// extension, defaulting to HTML.
func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	default:
		return "text/html; charset=utf-8"
	}
}
