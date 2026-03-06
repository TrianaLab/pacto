package doc

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"strings"
)

const htmlTemplate = `<!DOCTYPE html>
<html><head>
  <meta charset="utf-8">
  <title>{{TITLE}}</title>
  <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/github-markdown-css/5.8.1/github-markdown.min.css">
  <style>
    body { max-width: 980px; margin: 0 auto; padding: 45px; }
    @media (prefers-color-scheme: dark) {
      body { background: #0d1117; }
    }
  </style>
</head><body>
  <article class="markdown-body" id="content"></article>
  <script id="md" type="text/markdown">{{MARKDOWN}}</script>
  <script src="https://cdnjs.cloudflare.com/ajax/libs/marked/15.0.7/marked.min.js"></script>
  <script type="module">
    import mermaid from 'https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs';
    mermaid.initialize({ startOnLoad: false });
    const renderer = new marked.Renderer();
    const origCode = renderer.code.bind(renderer);
    renderer.code = function({ text, lang }) {
      if (lang === 'mermaid') {
        return '<pre class="mermaid">' + text + '</pre>';
      }
      return origCode({ text, lang });
    };
    const origHeading = renderer.heading.bind(renderer);
    renderer.heading = function({ tokens, depth }) {
      const text = this.parser.parseInline(tokens);
      const slug = text.replace(/<[^>]*>/g, '')
        .toLowerCase().replace(/[^\w\s-]/g, '').replace(/\s+/g, '-');
      return '<h' + depth + ' id="' + slug + '">' + text + '</h' + depth + '>\n';
    };
    marked.setOptions({ renderer });
    document.getElementById('content').innerHTML =
      marked.parse(document.getElementById('md').textContent);
    await mermaid.run({ nodes: document.querySelectorAll('.mermaid') });
  </script>
</body></html>`

// Serve starts a local HTTP server that renders the given markdown with
// GitHub-style styling using client-side marked.js. It blocks until the
// context is cancelled.
func Serve(ctx context.Context, markdown, title string, port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return ServeOnListener(ctx, markdown, title, ln)
}

// ServeOnListener is like Serve but accepts an existing net.Listener.
// This is useful in tests where port 0 is used to obtain a random port
// and the caller needs the address before blocking.
func ServeOnListener(ctx context.Context, markdown, title string, ln net.Listener) error {
	page := strings.Replace(htmlTemplate, "{{TITLE}}", html.EscapeString(title), 1)
	page = strings.Replace(page, "{{MARKDOWN}}", html.EscapeString(markdown), 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, page)
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
