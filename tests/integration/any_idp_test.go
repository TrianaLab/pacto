//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	ggcrauthn "github.com/google/go-containerregistry/pkg/authn"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// acmeReader is the only token the fake cluster review accepts: the private
// IDP's ServiceAccount, as a cluster TokenReview would name it.
const acmeReader = "acme-portal-reader"

// TestFakePrivateIDPEndToEnd runs tests/anyidp/acme-portal.sh, a private IDP
// that is neither Backstage nor any public portal, as written, with the
// shipped binary, against a real registry, a real Evidence Server and an
// in-process dashboard that reads Bearer tokens through the same authn
// middleware a cluster review feeds. It then checks what the script's
// contribution, target, export and provisioning report became, that silence
// reads Unknown and that acme-portal has its own producer row.
func TestFakePrivateIDPEndToEnd(t *testing.T) {
	scriptPath := filepath.Join(repoRoot, "tests", "anyidp", "acme-portal.sh")
	checkScriptHonesty(t, scriptPath)

	bin := buildPactoBinary(t)
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	registry := host + "/acme"
	home, work, keys := t.TempDir(), t.TempDir(), t.TempDir()
	env := append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home, "XDG_CACHE_HOME="+t.TempDir(), "PACTO_INSECURE_REGISTRIES="+host, "PACTO_NO_UPDATE_CHECK=1")
	pacto := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir, cmd.Env = work, env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("pacto %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	push := func(name, body string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.5\"\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
		var pushed struct{ Digest string }
		if err := json.Unmarshal([]byte(pacto("push", "oci://"+registry+"/"+name, "-p", dir, "--index", "none", "--output-format", "json")), &pushed); err != nil || pushed.Digest == "" {
			t.Fatalf("push %s: %v", name, err)
		}
		return "oci://" + registry + "/" + name + "@" + pushed.Digest
	}

	// The declared contracts, pushed the way CI would.
	db := "  - name: postgresql\n    ref: oci://" + registry + "/postgresql\n    required: true\n    compatibility: ^16\n"
	refs := []string{
		push("payments", "service:\n  name: payments\n  version: 2.1.0\n  owner: {team: payments}\n"),
		push("postgresql", "service:\n  name: postgresql\n  version: 16.1.0\n  owner: {team: data}\n  offering: postgresql\n"),
	}
	checkoutRef := push("checkout", "service:\n  name: checkout\n  version: 1.0.0\n  owner: {team: checkout}\n"+
		"dependencies:\n  - name: payments\n    ref: oci://"+registry+"/payments\n    required: true\n    compatibility: ^2\n"+db)
	refs = append(refs, checkoutRef,
		push("reports", "service:\n  name: reports\n  version: 1.0.0\n  owner: {team: reports}\ndependencies:\n"+db))

	// The platform holds acme-portal's public key and grants it before it
	// reports: the trust config the guide shows.
	pacto("evidence", "keygen", "--out", keys, "--producer", "acme-portal", "--key-id", "acme-portal-2026")
	raw, err := os.ReadFile(filepath.Join(repoRoot, "tests", "anyidp", "trust.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// publicKeyFile is a bare name in the config's own directory.
	trust := filepath.Join(keys, "trust.yaml")
	expanded := os.Expand(string(raw), func(k string) string { return map[string]string{"REGISTRY": registry}[k] })
	if err := os.WriteFile(trust, []byte(expanded), 0o600); err != nil {
		t.Fatal(err)
	}
	evidenceURL := serveEvidence(t, host, trust, "acme-portal", checkoutRef)
	pactoURL := serveAcmeDashboard(t, host, evidenceURL, refs)

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("Authorization: Bearer "+acmeReader+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runScript := func() {
		t.Helper()
		run := exec.Command("bash", "-euo", "pipefail", scriptPath)
		run.Dir = work
		run.Env = append(env, "REGISTRY="+registry, "EVIDENCE_URL="+evidenceURL, "PACTO_URL="+pactoURL,
			"PACTO_TOKEN_FILE="+tokenFile, "CHECKOUT_REF="+checkoutRef, "KEYS="+keys)
		out, err := run.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "acme-portal: done") {
			t.Fatalf("acme-portal.sh: %v\n%s", err, out)
		}
	}
	runScript()
	entities, err := os.ReadFile(filepath.Join(work, "entities.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	checkout := component(string(entities), "checkout")
	t.Logf("checkout's Component as acme-portal read it:\n%s", checkout)

	// No secret in a bundle: the contribution holds the two files only.
	store := oci.NewClient(ggcrauthn.NewMultiKeychain(), oci.WithInsecureRegistries(host))
	b, err := store.Pull(context.Background(), registry+"/pacto-inferred/acme-portal/services:legacy-billing_0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	if err := fs.WalkDir(b.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if slices.Sort(files); !slices.Equal(files, []string{"pacto.inferred.yaml", "pacto.yaml", "portal.json"}) {
		t.Errorf("legacy-billing's bundle holds %v, want exactly the record and the two files that describe it", files)
	}

	// A second run against the same registry and Evidence Server: the
	// wall-clock sequence base never replays.
	runScript()

	fleetArgs := []string{"--local", "", "--evidence-url", evidenceURL}
	for _, r := range refs {
		fleetArgs = append(fleetArgs, "--oci", r)
	}
	getJSON := func(name string) map[string]any {
		t.Helper()
		var v map[string]any
		if err := json.Unmarshal([]byte(pacto(append([]string{"fleet", "get", name, "--output-format", "json"}, fleetArgs...)...)), &v); err != nil {
			t.Fatalf("fleet get %s: %v", name, err)
		}
		return v
	}

	// Contribution (AC2.1): inferred, from acme-portal, never clearing a range.
	if out := pacto(append([]string{"fleet", "get", "legacy-billing"}, fleetArgs...)...); !strings.Contains(out, "Contributed by acme-portal") {
		t.Errorf("legacy-billing:\n%s", out)
	}
	if got := getJSON("legacy-billing")["service"].(map[string]any)["declaration"]; got != fleet.ProvenanceInferred {
		t.Errorf("legacy-billing declaration = %v, want inferred", got)
	}
	var graph struct {
		Edges []fleet.Relationship `json:"edges"`
	}
	if err := json.Unmarshal([]byte(pacto(append([]string{"fleet", "graph", "legacy-billing", "--output-format", "json"}, fleetArgs...)...)), &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Edges) != 1 || graph.Edges[0].To != "payments" || graph.Edges[0].Provenance != fleet.ProvenanceInferred ||
		graph.Edges[0].Compatibility != "unknown" || graph.Edges[0].RangeStatus == fleet.RangeSatisfied {
		t.Errorf("legacy-billing's edges = %+v, want one inferred edge to payments whose range is never satisfied", graph.Edges)
	}
	checkoutView := getJSON("checkout")
	targets, _ := checkoutView["targets"].([]any)
	if len(targets) != 1 || targets[0].(map[string]any)["reportedBy"] != "acme-portal" {
		t.Errorf("checkout's targets = %v, want one reported by acme-portal", targets)
	}

	// Export (AC2.2): the Bearer reader, the route's method and what it carries.
	for _, c := range []struct {
		method, auth string
		want         int
	}{{http.MethodGet, "", http.StatusUnauthorized}, {http.MethodGet, "Bearer wrong", http.StatusUnauthorized}, {http.MethodPost, "Bearer " + acmeReader, http.StatusMethodNotAllowed}} {
		req, _ := http.NewRequest(c.method, pactoURL+"/api/backstage/entities", nil)
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s with %q = %d, want %d", c.method, c.auth, resp.StatusCode, c.want)
		}
	}
	if !strings.Contains(checkout, `pacto.trianalab.io/dependency-ranges: '{"component:default/payments":[{"range":"^2","rangeStatus":"satisfied"}]`) {
		t.Errorf("checkout carries no satisfied ^2 range to payments:\n%s", checkout)
	}
	if want := "pacto.trianalab.io/conformance: " + fleet.StatusCompliant + "\n"; checkoutView["service"].(map[string]any)["status"] != fleet.StatusCompliant || !strings.Contains(checkout, want) {
		t.Errorf("checkout's conformance: fleet says %v, want %s on the route:\n%s", checkoutView["service"], want, checkout)
	}
	if pg := component(string(entities), "postgresql"); !strings.Contains(pg, "pacto.trianalab.io/offering: postgresql\n") {
		t.Errorf("postgresql names no offering:\n%s", pg)
	}
	lb := component(string(entities), "legacy-billing")
	for _, want := range []string{"pacto.trianalab.io/contributed-by: acme-portal\n", "pacto.trianalab.io/provenance: inferred\n", "pacto.trianalab.io/inferred-depends-on: "} {
		if !strings.Contains(lb, want) {
			t.Errorf("legacy-billing lacks %q:\n%s", want, lb)
		}
	}

	// Conformance (AC2.3): the report reads Ready; silence reads Unknown.
	var offering map[string]any
	if err := json.Unmarshal([]byte(pacto(append([]string{"fleet", "entity", "offering", "postgresql", "--output-format", "json"}, fleetArgs...)...)), &offering); err != nil {
		t.Fatal(err)
	}
	if p := provisioningOf(t, offering, "checkout"); p["phase"] != "Ready" || p["verified"] != true || p["producer"] != "acme-portal" {
		t.Errorf("checkout's postgresql provisioning = %v, want Ready, verified, from acme-portal", p)
	}
	if p := provisioningOf(t, offering, "reports"); p["phase"] != fleet.StatusUnknown || p["reason"] != "no provider has reported provisioning for this use" {
		t.Errorf("reports's postgresql provisioning = %v, want Unknown with no report", p)
	}

	// Detected providers (AC3): acme-portal is its own producer row.
	var providers fleet.ProvidersResult
	if err := json.Unmarshal([]byte(pacto(append([]string{"fleet", "providers", "--output-format", "json"}, fleetArgs...)...)), &providers); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(providers.Providers, func(p fleet.Provider) bool { return p.ID == fleet.ProducerRowPrefix+"acme-portal" })
	if i < 0 {
		t.Fatalf("no producer/acme-portal row in %+v", providers.Providers)
	}
	row, _ := json.Marshal(providers.Providers[i])
	t.Logf("acme-portal's producer row: %s", row)
	if p := providers.Providers[i]; p.Status != fleet.SourceAvailable || !slices.Contains(p.UsedFor, "reported-targets") || !slices.Contains(p.UsedFor, "contributed-records") {
		t.Errorf("acme-portal's row = %s, want available and used for its targets and its contributed records", row)
	}
}

// checkScriptHonesty holds the script to its contract: under 100 lines, only
// curl and these pacto subcommands, only the two public paths, no other tool
// and no token in its text.
func checkScriptHonesty(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	if n := strings.Count(script, "\n"); n > 99 {
		t.Errorf("acme-portal.sh is %d lines, at most 99", n)
	}
	allowed := []string{"pacto push", "pacto evidence keygen", "pacto evidence sign", "pacto evidence send", "pacto evidence provisioned"}
	for _, m := range regexp.MustCompile(`\bpacto [a-z]+( [a-z]+)?`).FindAllString(script, -1) {
		if !slices.ContainsFunc(allowed, func(a string) bool { return m == a || strings.HasPrefix(m, "pacto push ") }) {
			t.Errorf("acme-portal.sh runs %q", m)
		}
	}
	for _, m := range regexp.MustCompile(`/api/[a-z0-9/]+`).FindAllString(script, -1) {
		if m != "/api/backstage/entities" && m != "/api/evidence/v1/envelopes" {
			t.Errorf("acme-portal.sh calls %s", m)
		}
	}
	if m := regexp.MustCompile(`(?m)^\s*(oras|jq|go)\b|\|\s*(oras|jq|go)\b|\$\((oras|jq|go)\b`).FindString(script); m != "" {
		t.Errorf("acme-portal.sh calls %q", m)
	}
	if strings.Contains(script, "Bearer $") || strings.Contains(script, "--local") {
		t.Error("acme-portal.sh puts the token in argv or reads Pacto internals")
	}
}

// serveAcmeDashboard starts an in-process dashboard whose fleet is rebuilt on
// every read over refs and the Evidence Server, behind a sign-in that takes
// only acmeReader as a Bearer token, and returns its base URL.
func serveAcmeDashboard(t *testing.T, host, evidenceURL string, refs []string) string {
	t.Helper()
	review := func(_ context.Context, token string) (authn.Reviewed, error) {
		if token != acmeReader {
			return authn.Reviewed{}, errors.New("the cluster does not know this token")
		}
		return authn.Reviewed{Person: authn.Person{User: "system:serviceaccount:acme:acme-portal", Groups: []string{"system:serviceaccounts"}}, Bearer: true}, nil
	}
	d := authn.NewShared(nil, authn.NewTokenLogin(review, authn.Lifetime{})).AcceptBearer(review)
	svc := app.NewService(oci.NewClient(ggcrauthn.NewMultiKeychain(), oci.WithInsecureRegistries(host)), nil)
	o := dashboard.Options{
		Fleet: func(ctx context.Context) (*fleet.Query, error) {
			snap, err := svc.Fleet(ctx, app.FleetOptions{EvidenceURLs: []string{evidenceURL}, OCIRefs: refs})
			if err != nil {
				return nil, err
			}
			return fleet.NewQuery(snap), nil
		},
		Auth: &dashboard.Auth{TokenLogin: true, Routes: d.Routes(), Middleware: d.Middleware,
			State: func(r *http.Request) dashboard.AuthState {
				st := authn.StateFrom(r.Context())
				return dashboard.AuthState{SignedIn: st.SignedIn, User: st.Person.User, Groups: st.Person.Groups, Method: st.Method}
			}},
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dashboard.NewServer(o).ServeOnListener(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("dashboard: %v", err)
		}
	})
	return "http://" + ln.Addr().String()
}

// component is the Component named name in a multi-document entities export.
func component(entities, name string) string {
	for doc := range strings.SplitSeq(entities, "\n---\n") {
		if strings.Contains(doc, "kind: Component\n") && strings.Contains(doc, "\n  name: "+name+"\n") {
			return doc
		}
	}
	return ""
}

// provisioningOf is the one provisioning status of consumer's use in an
// offering's JSON page, found wherever the page lists its uses.
func provisioningOf(t *testing.T, page any, consumer string) map[string]any {
	t.Helper()
	var found []map[string]any
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			c, _ := v["consumer"].(map[string]any)
			if prov, ok := v["provisioning"].([]any); ok && c != nil && c["label"] == consumer && len(prov) == 1 {
				found = append(found, prov[0].(map[string]any))
				return
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(page)
	if len(found) != 1 {
		b, _ := json.MarshalIndent(page, "", " ")
		t.Fatalf("%d single provisioning statuses for %s in:\n%s", len(found), consumer, b)
	}
	return found[0]
}
