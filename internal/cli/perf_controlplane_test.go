//go:build perfenvtest

package cli

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

// controlPlane is a real etcd and kube-apiserver from the envtest assets
// (KUBEBUILDER_ASSETS, as `make -C integrations/kubernetes setup-envtest`
// prints it). People authenticate with static bearer tokens shaped like the
// ID tokens the dashboard passes through, so the apiserver and the
// dashboard's own reading of a token name the same person.
// ponytail: started by hand rather than through controller-runtime's envtest
// package, which the root module does not depend on; the flags are envtest's.
type controlPlane struct {
	host  string
	admin string // token of a system:masters user: the dashboard's own credentials
}

// perfPerson is a signed-in person: the apiserver user is issuer#sub, as
// the delivery identity docs map an OIDC token.
type perfPerson struct {
	name   string
	groups []string
	token  string
}

const perfIssuer = "https://idp.example"

func (p perfPerson) user() string { return perfIssuer + "#" + p.name }

// idToken is an unsigned token shaped like an ID token: the apiserver reads
// it from the token file, the dashboard reads its claims.
func idToken(sub string, groups []string) string {
	enc := base64.RawURLEncoding.EncodeToString
	payload, _ := json.Marshal(map[string]any{"iss": perfIssuer, "sub": sub, "groups": groups, "name": sub})
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + ".perf"
}

func freePort(t testing.TB) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func startControlPlane(t testing.TB, people []perfPerson) *controlPlane {
	t.Helper()
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set: run make perf-envtest")
	}
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	saKey := filepath.Join(dir, "sa.key")
	must(t, os.WriteFile(saKey, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600))
	cp := &controlPlane{admin: "perf-admin-token"}
	lines := []string{cp.admin + `,perf-admin,perf-admin,"system:masters"`}
	for _, p := range people {
		lines = append(lines, fmt.Sprintf(`%s,%s,%s,"%s"`, p.token, p.user(), p.name, strings.Join(p.groups, ",")))
	}
	tokens := filepath.Join(dir, "tokens.csv")
	must(t, os.WriteFile(tokens, []byte(strings.Join(lines, "\n")+"\n"), 0o600))

	etcdPort, peerPort, apiPort := freePort(t), freePort(t), freePort(t)
	etcdURL := fmt.Sprintf("http://127.0.0.1:%d", etcdPort)
	run(t, filepath.Join(assets, "etcd"), "--data-dir", filepath.Join(dir, "etcd"),
		"--listen-client-urls", etcdURL, "--advertise-client-urls", etcdURL,
		"--listen-peer-urls", fmt.Sprintf("http://127.0.0.1:%d", peerPort), "--unsafe-no-fsync", "--quota-backend-bytes", "8589934592")
	run(t, filepath.Join(assets, "kube-apiserver"), "--etcd-servers", etcdURL,
		"--advertise-address", "127.0.0.1", "--bind-address", "127.0.0.1", "--secure-port", fmt.Sprint(apiPort),
		"--cert-dir", filepath.Join(dir, "certs"), "--service-cluster-ip-range", "10.0.0.0/24", "--allow-privileged=true",
		"--disable-admission-plugins", "ServiceAccount", "--endpoint-reconciler-type", "none", "--authorization-mode", "RBAC", "--token-auth-file", tokens,
		"--service-account-issuer", "https://kubernetes.default.svc", "--service-account-key-file", saKey,
		"--service-account-signing-key-file", saKey, "--max-requests-inflight", "2000", "--max-mutating-requests-inflight", "1000")
	cp.host = fmt.Sprintf("https://127.0.0.1:%d", apiPort)
	cl := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // a throwaway local apiserver
	deadline := time.Now().Add(90 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, cp.host+"/readyz", nil)
		req.Header.Set("Authorization", "Bearer "+cp.admin)
		if resp, err := cl.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("kube-apiserver did not become ready")
		}
		time.Sleep(250 * time.Millisecond)
	}
	return cp
}

func run(t testing.TB, bin string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	if os.Getenv("PERF_VERBOSE") != "" {
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	}
	must(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
}

// config is a client of the apiserver with token as the bearer; an empty
// token is the cluster's address alone; qps 0 is client-go's default
// limit, as the dashboard runs with, and -1 none.
func (cp *controlPlane) config(token string, qps float32) *rest.Config {
	return &rest.Config{Host: cp.host, BearerToken: token, TLSClientConfig: rest.TLSClientConfig{Insecure: true}, QPS: qps, WarningHandler: rest.NoWarnings{}}
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
