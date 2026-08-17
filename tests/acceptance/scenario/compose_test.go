package scenario

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var composeOpts = ComposeOptions{
	PactoImage:    "ghcr.io/trianalab/pacto/dashboard:9.9.9",
	RegistryImage: "registry:2",
}

// composeFileOf decodes the projection the way Docker Compose reads it, so every
// assertion below is about the file's MEANING and not about the text that
// happened to produce it.
func composeFileOf(t *testing.T, s Scenario) map[string]any {
	t.Helper()
	body, err := s.Compose(composeOpts)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	var f map[string]any
	if err := yaml.Unmarshal(body, &f); err != nil {
		t.Fatalf("the projection is not YAML Compose could read: %v\n%s", err, body)
	}
	return f
}

func composeSvc(t *testing.T, f map[string]any, name string) map[string]any {
	t.Helper()
	services, ok := f["services"].(map[string]any)
	if !ok {
		t.Fatalf("the projection declares no services: %v", f)
	}
	svc, ok := services[name].(map[string]any)
	if !ok {
		t.Fatalf("the projection declares no %q service (has: %v)", name, keysOfAny(services))
	}
	return svc
}

func keysOfAny(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// listOf is a service's string list, absent or not: a service that declares no
// ports has none, which is a fact about the projection and not a decoding error.
func listOf(t *testing.T, svc map[string]any, key string) []string {
	t.Helper()
	raw, ok := svc[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, len(raw))
	for i, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s[%d] is %T, not a string", key, i, v)
		}
		out[i] = s
	}
	return out
}

// argsOf is a service's command as one string, for asking whether an argument is
// there. Compose commands are exec-form lists, so the join is only for searching.
func argsOf(t *testing.T, svc map[string]any, key string) string {
	t.Helper()
	raw, ok := svc[key]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, len(v))
		for i, p := range v {
			s, ok := p.(string)
			if !ok {
				t.Fatalf("%s[%d] is %T, not a string", key, i, p)
			}
			parts[i] = s
		}
		return strings.Join(parts, " ")
	default:
		t.Fatalf("%s is %T", key, raw)
		return ""
	}
}

// The dashboard reads the registry the demo brings up, and it is told about
// exactly the services whose published revisions the Product gate will require.
// The EvidenceOnly service is NOT among them: its bundle is published so a signed
// envelope can point at real content, and the Product must learn about it only
// from the Evidence Server. Listing its repository here would give it OCI
// revisions in the fixture's own domain — a second identity for one service,
// which is the duplicate the gate refuses.
func TestCompose_TellsTheDashboardExactlyTheOCISourcesTheGateWillProve(t *testing.T) {
	cmd := argsOf(t, composeSvc(t, composeFileOf(t, OperationalGraph), "dashboard"), "command")
	named := strings.Fields(cmd)
	for _, svc := range OperationalGraph.Services {
		ref := "oci://" + ComposeDomain + "/" + svc.Repo
		if svc.EvidenceOnly {
			if strings.Contains(cmd, ref) {
				t.Errorf("the dashboard is pointed at %s, which the Product must only learn about over the Evidence Server", ref)
			}
			continue
		}
		if !slices.Contains(named, ref) {
			t.Errorf("the dashboard is not pointed at %s; its revisions could not be proved:\n%s", ref, cmd)
		}
		// And at each one BY VERSION. The repository alone resolves to the newest
		// tag, so a superseded revision would reach the Product from the disk cache
		// and never from the registry — two sources disagreeing about what was
		// published, which is exactly what the gate refuses to call complete.
		for _, rev := range svc.Revisions {
			if !slices.Contains(named, ref+":"+rev.Version) {
				t.Errorf("the dashboard is not pointed at %s:%s, so only the newest revision would reach it from the registry:\n%s", ref, rev.Version, cmd)
			}
		}
	}
}

// A published service nothing runs still belongs in the snapshot — the gate
// requires a canonical revision for every service that is not EvidenceOnly,
// whether or not a workload exists. Deriving the list from "runs something" would
// pass today, because in this fixture the two sets coincide.
func TestCompose_ASourceIsOwedByPublishing_NotByRunning(t *testing.T) {
	s := mutate(func(s *Scenario) {
		s.Services = append(s.Services, Service{
			Name: "catalog", Repo: "catalog",
			Revisions: []Revision{{Version: "1.0.0", Dir: "catalog", Files: map[string]string{"pacto.yaml": "x"}}},
		})
	})
	cmd := argsOf(t, composeSvc(t, composeFileOf(t, s), "dashboard"), "command")
	if !strings.Contains(cmd, "oci://"+ComposeDomain+"/catalog ") {
		t.Errorf("a published service that runs nowhere was left out of the dashboard's sources:\n%s", cmd)
	}
}

// Every observation source the scenario declares is mounted and named, with the
// SAME stable identity the Kubernetes surface configures through the chart. The
// path is inside the artifact, which is the only thing the run directory holds.
func TestCompose_NamesEveryObservationSourceWithItsStableIdentity(t *testing.T) {
	cmd := argsOf(t, composeSvc(t, composeFileOf(t, OperationalGraph), "dashboard"), "command")
	n := 0
	for _, src := range OperationalGraph.Sources {
		if src.Kind != SourceObservation {
			continue
		}
		n++
		want := src.ID + "=" + ComposeArtifactMount + "/" + src.ID + ".json"
		if !strings.Contains(cmd, want) {
			t.Errorf("the dashboard is not configured with the observation source %q:\n%s", want, cmd)
		}
	}
	if n == 0 {
		t.Fatal("the fixture declares no observation source, so this proves nothing")
	}
}

// WHO signs is the scenario's to declare, on both surfaces. The Evidence Server
// mints the keypair for the declared producer at run time; sign as anyone else
// and ingestion rejects it.
func TestCompose_MintsTheDeclaredSignerIdentity(t *testing.T) {
	signer, err := OperationalGraph.signer()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	svc := composeSvc(t, composeFileOf(t, OperationalGraph), "evidence")
	cmd := argsOf(t, svc, "command") + argsOf(t, svc, "entrypoint")
	for _, want := range []string{signer.Producer, signer.KeyID} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the Evidence Server does not mint the declared signer %q:\n%s", want, cmd)
		}
	}
}

// The projection FOLLOWS the declaration. Rename the signer and the container
// that mints its key must mint the new one, with nothing left of the old.
func TestCompose_FollowsTheDeclaredSigner(t *testing.T) {
	before, err := OperationalGraph.Compose(composeOpts)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	after, err := mutate(func(s *Scenario) {
		s.Evidence[0].Signer = Signer{Producer: "other-collector", KeyID: "other"}
	}).Compose(composeOpts)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	moved(t, string(before), string(after), "remote-eu-collector", "other-collector")
}

// NOTHING secret is in the artifact. The keypair is generated at run time into a
// volume the artifact never contains, so an artifact that shipped a key — or a
// projection that grew a password, token or inline private key — is a different
// artifact from the one this demo distributes.
func TestCompose_EmbedsNoCredential(t *testing.T) {
	body, err := OperationalGraph.Compose(composeOpts)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	assertNoSecretMaterial(t, "compose.yaml", body)
	// And the keypair is minted where the artifact cannot reach: under the state
	// volume, not under the read-only mount of the run directory.
	cmd := argsOf(t, composeSvc(t, composeFileOf(t, OperationalGraph), "evidence"), "command")
	if !strings.Contains(cmd, "keygen") {
		t.Error("the demo never generates a keypair, so it must be shipping one")
	}
	if strings.Contains(cmd, "keygen --out "+ComposeArtifactMount) {
		t.Error("the keypair is written into the artifact mount rather than the state volume")
	}
}

// secretMarkers are what a credential looks like in a file that must hold none.
var secretMarkers = []*regexp.Regexp{
	regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\b(password|passwd|secret_key|api[_-]?key|access[_-]?token|bearer)\b\s*[:=]`),
	regexp.MustCompile(`(?i)\bAWS_SECRET_ACCESS_KEY\b`),
	// A bare 44-character base64 blob is what `evidence keygen` writes an Ed25519
	// seed out as, so any such run is treated as key material.
	regexp.MustCompile(`(?m)^[A-Za-z0-9+/]{43}=$`),
}

func assertNoSecretMaterial(t *testing.T, name string, body []byte) {
	t.Helper()
	for _, re := range secretMarkers {
		if m := re.Find(body); m != nil {
			t.Errorf("%s carries what looks like a credential: %q", name, m)
		}
	}
}

// Every host port is a deterministic default that the environment can override,
// and the shipped .env states the SAME defaults. A user who edits .env and a user
// who exports the variable must get the same demo.
func TestCompose_PublishesOverridablePortsThatAgreeWithTheEnvFile(t *testing.T) {
	f := composeFileOf(t, OperationalGraph)
	env := string(ComposeEnv())
	if len(ComposePorts()) == 0 {
		t.Fatal("the demo publishes no port at all")
	}
	for _, p := range ComposePorts() {
		ports := listOf(t, composeSvc(t, f, p.Service), "ports")
		want := "${" + p.Env + ":-" + strconv.Itoa(p.Default) + "}:" + strconv.Itoa(p.Container)
		if !slices.Contains(ports, want) {
			t.Errorf("service %s publishes %v, want %q", p.Service, ports, want)
		}
		if line := p.Env + "=" + strconv.Itoa(p.Default); !strings.Contains(env, line+"\n") {
			t.Errorf(".env does not carry %q:\n%s", line, env)
		}
	}
	// Nothing is published that the file did not declare overridable: a hardcoded
	// host port is the one thing a second demo on the same machine cannot work
	// around.
	for name, raw := range f["services"].(map[string]any) {
		svc, _ := raw.(map[string]any)
		for _, p := range listOf(t, svc, "ports") {
			if !strings.HasPrefix(p, "${") {
				t.Errorf("service %s publishes the fixed host port %q", name, p)
			}
		}
	}
}

// Nothing pins an architecture. Both images the demo runs are multi-platform, and
// a `platform:` key would silently emulate one of them on the other.
func TestCompose_PinsNoArchitecture(t *testing.T) {
	f := composeFileOf(t, OperationalGraph)
	for name, raw := range f["services"].(map[string]any) {
		svc, _ := raw.(map[string]any)
		if v, ok := svc["platform"]; ok {
			t.Errorf("service %s pins platform %v; the images are multi-arch", name, v)
		}
	}
}

// Two pulled versions are two demos, not one demo twice.
//
// Compose scopes containers and volumes by PROJECT, and takes the project name
// from the run directory unless the file pins one. Pinning one would make the
// documented upgrade — pull the next version into a fresh directory and start it
// — quietly reuse the previous version's registry content and ingested evidence,
// so the new demo would be showing the old demo's state and going back would have
// nothing to go back to.
func TestCompose_ScopesItsStateToTheRunDirectory(t *testing.T) {
	if name, pinned := composeFileOf(t, OperationalGraph)["name"]; pinned {
		t.Errorf("the projection pins the Compose project name to %q, so a second pulled version would share this one's volumes", name)
	}
}

// Both images the demo runs ship multi-architecture indexes, so Docker resolves
// whichever one the host is. A `platform:` key would override that resolution and
// silently hand an Apple Silicon or arm64 server user an emulated amd64 stack —
// the plausible "fix" for one arm64 bug, and a permanent tax on everybody else.
// The demo has no reason to care what it runs on, so it may not say.
func TestCompose_LetsTheHostArchitectureDecide(t *testing.T) {
	f := composeFileOf(t, OperationalGraph)
	services, ok := f["services"].(map[string]any)
	if !ok {
		t.Fatal("the projection declares no services")
	}
	for _, name := range keysOfAny(services) {
		if p, pinned := composeSvc(t, f, name)["platform"]; pinned {
			t.Errorf("%s pins platform %v, so every other architecture runs it emulated", name, p)
		}
	}
}

// After the images are on the host, the demo reaches no registry but the one it
// starts itself. Every OCI reference in the projection is to that service.
func TestCompose_ResolvesOnlyAgainstTheRegistryItStarts(t *testing.T) {
	body, err := OperationalGraph.Compose(composeOpts)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for _, ref := range regexp.MustCompile(`oci://[^\s"']+`).FindAllString(string(body), -1) {
		if !strings.HasPrefix(ref, "oci://"+ComposeRegistryHost+"/") {
			t.Errorf("the demo resolves %s, which is not the registry it starts", ref)
		}
	}
}

// ...and every Pacto process is told that registry speaks plain HTTP.
//
// Not just the ones with an `oci://` on their command line. The Evidence Server
// has none and still resolves one: ingestion re-resolves each envelope's contract
// ref before accepting it, so a server left to assume TLS answers 502 on a demo
// whose registry has no certificate — a failure that surfaces as a rejected
// envelope several services away from its cause. The predicate is therefore "runs
// Pacto", not "names a repository".
func TestCompose_TellsEveryPactoProcessTheRegistryIsPlainHTTP(t *testing.T) {
	file := composeFileOf(t, OperationalGraph)
	services, ok := file["services"].(map[string]any)
	if !ok {
		t.Fatalf("compose file has no services map")
	}
	n := 0
	for name := range services {
		svc := composeSvc(t, file, name)
		if svc["image"] != composeOpts.PactoImage {
			continue
		}
		n++
		env, _ := svc["environment"].(map[string]any)
		if env["PACTO_INSECURE_REGISTRIES"] != ComposeRegistryHost {
			t.Errorf("%s runs pacto against %s without being told it is plain HTTP; it got %v", name, ComposeRegistryHost, env["PACTO_INSECURE_REGISTRIES"])
		}
	}
	if n == 0 {
		t.Fatal("no service runs pacto, so this proves nothing")
	}
}

// An image is a runtime input the projection cannot invent. Defaulting one would
// ship a demo pinned to whatever `latest` meant on the day it ran.
func TestCompose_RefusesToInventAnImage(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts ComposeOptions
	}{
		{"no pacto image", ComposeOptions{RegistryImage: "registry:2"}},
		{"no registry image", ComposeOptions{PactoImage: "pacto:1"}},
		{"neither", ComposeOptions{}},
	} {
		// The refusal has to SAY it is about the image: a missing one otherwise
		// surfaces further down as an empty value in a command, which reads like a
		// broken scenario rather than a caller that forgot an argument.
		switch _, err := OperationalGraph.Compose(tc.opts); {
		case err == nil:
			t.Errorf("%s: the projection invented an image", tc.name)
		case !strings.Contains(err.Error(), "image"):
			t.Errorf("%s: refused with %q, which does not say an image is missing", tc.name, err)
		}
	}
}

// A value the scenario declares becomes an argument in a generated command, so
// the delimiter set that could turn one argument into two — or one command into
// two — is refused by name rather than escaped into something unrecognisable.
func TestCompose_RefusesAValueThatCouldForgeACommand(t *testing.T) {
	for _, bad := range []string{"a b", "a;b", "a\nb", "a$b", "a|b", "a'b", `a"b`, "a`b", ""} {
		_, err := mutate(func(s *Scenario) { s.Evidence[0].Signer.Producer = bad }).Compose(composeOpts)
		if err == nil {
			t.Errorf("the producer %q was accepted into a generated command", bad)
		}
	}
}

// Startup is ORDERED by observed readiness, not by waiting. The dashboard's first
// snapshot must be able to see published bundles and an ingested envelope, so it
// starts only after the one-shot seed has succeeded — and the seed only after the
// Evidence Server reports itself ready.
func TestCompose_OrdersStartupOnObservedReadiness(t *testing.T) {
	f := composeFileOf(t, OperationalGraph)
	for _, tc := range []struct{ service, dependsOn, condition string }{
		{"dashboard", "seed", "service_completed_successfully"},
		{"seed", "evidence", "service_healthy"},
		{"seed", "registry", "service_started"},
	} {
		deps, ok := composeSvc(t, f, tc.service)["depends_on"].(map[string]any)
		if !ok {
			t.Fatalf("service %s depends on nothing", tc.service)
		}
		on, ok := deps[tc.dependsOn].(map[string]any)
		if !ok {
			t.Fatalf("service %s does not depend on %s", tc.service, tc.dependsOn)
		}
		if got := on["condition"]; got != tc.condition {
			t.Errorf("%s waits for %s on %v, want %s", tc.service, tc.dependsOn, got, tc.condition)
		}
	}
	// The Evidence Server states its own readiness rather than inheriting the
	// dashboard probe baked into the shared image, which would never pass here and
	// would hang `up --wait` until it timed out.
	hc, ok := composeSvc(t, f, "evidence")["healthcheck"].(map[string]any)
	if !ok {
		t.Fatal("the Evidence Server declares no healthcheck")
	}
	if test := argsOf(t, hc, "test"); !strings.Contains(test, "/api/evidence/v1/ready") {
		t.Errorf("the Evidence Server's healthcheck is %q, which is not its readiness endpoint", test)
	}
}

// State that must survive a restart is in a named volume, and the artifact is
// mounted READ-ONLY: nothing the demo writes can end up in the run directory, so
// a second run from the same pinned artifact starts from the same bytes.
func TestCompose_KeepsMutableStateOutOfTheArtifact(t *testing.T) {
	f := composeFileOf(t, OperationalGraph)
	vols, ok := f["volumes"].(map[string]any)
	if !ok || len(vols) == 0 {
		t.Fatal("the demo declares no named volume, so nothing it writes survives a restart")
	}
	services, _ := f["services"].(map[string]any)
	mounts := 0
	for name, raw := range services {
		svc, _ := raw.(map[string]any)
		for _, m := range listOf(t, svc, "volumes") {
			source, rest, _ := strings.Cut(m, ":")
			if source == "." {
				mounts++
				if !strings.HasSuffix(rest, ":ro") {
					t.Errorf("service %s mounts the run directory writable (%q)", name, m)
				}
				continue
			}
			if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "/") {
				t.Errorf("service %s bind-mounts %q; the run directory is all the demo may read", name, m)
			}
			if _, declared := vols[source]; !declared {
				t.Errorf("service %s mounts the undeclared volume %q", name, source)
			}
		}
	}
	if mounts == 0 {
		t.Fatal("nothing mounts the run directory, so the artifact is never read")
	}
}
