package dashboard

import (
	"net/http"
	"strings"
	"testing"
)

// outsideServer serves a fake Outside handler beside a session guard that
// refuses every request and counts the requests it saw.
func outsideServer(t *testing.T, guarded *int) string {
	t.Helper()
	guard := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			*guarded++
			http.Error(w, "sign in", http.StatusUnauthorized)
		})
	}
	outside := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("outside")) })
	return serveOptions(t, Options{Fleet: fleetOf(viewQuery(t)), Auth: &Auth{Routes: http.NotFoundHandler(), Middleware: guard},
		Outside: map[string]http.Handler{"/agents": outside}})
}

func TestOutsideRoutesSkipSessionGuard(t *testing.T) {
	var guarded int
	base := outsideServer(t, &guarded)
	resp, err := http.Post(base+"/agents", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || guarded != 0 {
		t.Fatalf("outside route: %d, guard ran %d times", resp.StatusCode, guarded)
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("the security headers wrap an outside route: %v", resp.Header)
	}
	expectStatus(t, base+"/api/fleet/services", http.StatusUnauthorized)
	if guarded != 1 {
		t.Errorf("the fleet API went past the session guard: %d", guarded)
	}
}

func TestOutsideRouteCrossOriginRefused(t *testing.T) {
	var guarded int
	base := outsideServer(t, &guarded)
	r, _ := http.NewRequest(http.MethodPost, base+"/agents", strings.NewReader("{}"))
	r.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a cross-origin POST to an outside route: %d", resp.StatusCode)
	}
}
