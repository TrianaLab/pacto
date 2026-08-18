package scenario

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The references the projection is exercised with. Digest-qualified, because
// that is the only form it accepts: the pacto image stands in for whatever the
// release transaction published, the registry image is the real pin the demo
// ships with.
var composeOpts = ComposeOptions{
	PactoImage:    "ghcr.io/trianalab/pacto/dashboard@sha256:" + strings.Repeat("9", 64),
	RegistryImage: ComposeDefaultRegistryImage,
	Version:       "9.9.9",
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
			Revisions: []Revision{{Version: "1.0.0", Dir: "catalog", Files: map[string]string{
				"pacto.yaml": `pactoVersion: "2.0"
service: { name: catalog, version: "1.0.0" }
workload: service
state: { type: stateless, persistence: { scope: local, durability: ephemeral }, dataCriticality: low }
`,
			}}},
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

// The Evidence Server is configured with EXACT contract revisions and stores its
// accepted records against them in the registry — so the artifact carries no
// place for evidence to live, and none of its volumes is one.
func TestCompose_StoresEvidenceInTheRegistry(t *testing.T) {
	body, err := OperationalGraph.Compose(composeOpts)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	f := composeFileOf(t, OperationalGraph)
	cmd := argsOf(t, composeSvc(t, f, "evidence"), "command")
	subjects := composeEvidenceSubjects(t, OperationalGraph)
	if len(subjects) == 0 {
		t.Fatal("the Evidence Server is configured with no subject, so it could store nothing")
	}
	for _, subj := range subjects {
		// An exact digest, never a tag: a tag can be moved onto another manifest and
		// the evidence stored under it would silently come to describe other content.
		if !strings.Contains(subj, "@sha256:") {
			t.Errorf("subject %q is not an exact contract revision", subj)
		}
	}
	if strings.Contains(cmd, "--bucket-url") || strings.Contains(cmd, "--store-dir") {
		t.Errorf("the Evidence Server still has a local store:\n%s", cmd)
	}
	// The state volume stays — it holds the trust store and the minted key — but
	// nothing in the artifact names an evidence directory any more.
	if strings.Contains(string(body), composeStateMount+"/evidence") {
		t.Error("the artifact still mounts an evidence data directory")
	}
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

// composeEvidenceSubjects is every contract revision the Evidence Server
// container is configured with, read back out of its generated command.
func composeEvidenceSubjects(t *testing.T, s Scenario) []string {
	t.Helper()
	cmd := argsOf(t, composeSvc(t, composeFileOf(t, s), "evidence"), "command")
	fields := strings.Fields(cmd)
	var out []string
	for i, f := range fields {
		if f == "--subject" && i+1 < len(fields) {
			out = append(out, fields[i+1])
		}
	}
	slices.Sort(out)
	return out
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

// portsOf is a service's published ports, in the long form the projection emits.
func portsOf(t *testing.T, svc map[string]any) []map[string]any {
	t.Helper()
	raw, _ := svc["ports"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for i, v := range raw {
		p, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("ports[%d] is %T, not the long form `docker compose publish` accepts", i, v)
		}
		out = append(out, p)
	}
	return out
}

// Every host port is a deterministic default the environment can override.
//
// There is no .env to state them in twice: a published application is one file,
// so the defaults live in the interpolation itself and a user who exports the
// variable and a user who does not get the same demo on different ports. The
// override is what lets two pulled versions run at once.
func TestCompose_PublishesOverridablePortsInTheFormPublishAccepts(t *testing.T) {
	f := composeFileOf(t, OperationalGraph)
	if len(ComposePorts()) == 0 {
		t.Fatal("the demo publishes no port at all")
	}
	for _, p := range ComposePorts() {
		ports := portsOf(t, composeSvc(t, f, p.Service))
		want := map[string]any{
			"target":    p.Container,
			"published": "${" + p.Env + ":-" + strconv.Itoa(p.Default) + "}",
			"protocol":  "tcp",
			"mode":      "ingress",
		}
		if !slices.ContainsFunc(ports, func(got map[string]any) bool { return maps.Equal(got, want) }) {
			t.Errorf("service %s publishes %v, want %v", p.Service, ports, want)
		}
	}
	// Nothing is published that the file did not declare overridable: a hardcoded
	// host port is the one thing a second demo on the same machine cannot work
	// around.
	for name, raw := range f["services"].(map[string]any) {
		svc, _ := raw.(map[string]any)
		for _, p := range portsOf(t, svc) {
			if pub, _ := p["published"].(string); !strings.HasPrefix(pub, "${") {
				t.Errorf("service %s publishes the fixed host port %q", name, pub)
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
// Compose scopes containers and volumes by PROJECT. An application run from
// `-f oci://…` has no run directory to derive a project name from, so the name is
// whatever the user's `-p` says — and the documented journey always says one.
// Pinning a `name:` here would take that away: the second version pulled would
// silently reuse the first's registry content and ingested evidence, so "upgrade"
// would mean "run the new demo over the old demo's state" and going back would
// have nothing to go back to. It would collide across USERS of one artifact on one
// machine too, which even a run directory could not do.
func TestCompose_ScopesItsStateToTheUsersProjectName(t *testing.T) {
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
// starts itself. Every OCI reference the services are CONFIGURED with is to that
// service.
//
// The services and not the whole file: the seed script travels inline now, and it
// builds its references out of $PACTO_DEMO_PUBLISH_TO, which is that same
// registry — read as text those look like a reference to a host called
// "$PACTO_DEMO_PUBLISH_TO". The environment it reads is checked here instead,
// which is the value that decides where the push lands.
func TestCompose_ResolvesOnlyAgainstTheRegistryItStarts(t *testing.T) {
	f := composeFileOf(t, OperationalGraph)
	refs := regexp.MustCompile(`oci://[^\s"']+`)
	n := 0
	for name, raw := range f["services"].(map[string]any) {
		svc, _ := raw.(map[string]any)
		configured := argsOf(t, svc, "command") + " " + argsOf(t, svc, "entrypoint")
		env, _ := svc["environment"].(map[string]any)
		for _, v := range env {
			configured += " " + fmt.Sprint(v)
		}
		for _, ref := range refs.FindAllString(configured, -1) {
			n++
			if !strings.HasPrefix(ref, "oci://"+ComposeRegistryHost+"/") {
				t.Errorf("service %s resolves %s, which is not the registry the demo starts", name, ref)
			}
		}
		if got := env["PACTO_DEMO_PUBLISH_TO"]; got != nil && got != ComposeDomain {
			t.Errorf("service %s publishes to %v, which is not the registry the demo starts", name, got)
		}
	}
	if n == 0 {
		t.Fatal("no service is configured with an OCI reference, so this proves nothing")
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
		{"no pacto image", ComposeOptions{RegistryImage: composeOpts.RegistryImage}},
		{"no registry image", ComposeOptions{PactoImage: composeOpts.PactoImage}},
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

// An image reference a tag could move is refused, fail-closed.
//
// The artifact is published, pulled and documented BY DIGEST, so its identity is
// immutable everywhere except the two image references inside it. Leave either
// one on a tag and one unchanged demo artifact executes different bytes the day
// after the tag moves, with nothing about the artifact saying so — the pin is
// worth exactly as much as its weakest reference. The refusal names the option,
// because the caller that has to fix it is a release step several files away.
func TestCompose_RefusesAnImageATagCouldMove(t *testing.T) {
	pinnedPacto, pinnedRegistry := composeOpts.PactoImage, composeOpts.RegistryImage
	sixtyFour := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, says string
		opts       ComposeOptions
	}{
		{"tag-only pacto image", "pacto image",
			ComposeOptions{PactoImage: "ghcr.io/trianalab/pacto/dashboard:1.2.3", RegistryImage: pinnedRegistry}},
		{"pacto image with no tag at all", "pacto image",
			ComposeOptions{PactoImage: "ghcr.io/trianalab/pacto/dashboard", RegistryImage: pinnedRegistry}},
		{"tag-only registry image", "registry image",
			ComposeOptions{PactoImage: pinnedPacto, RegistryImage: "registry:2"}},
		{"registry image with no tag at all", "registry image",
			ComposeOptions{PactoImage: pinnedPacto, RegistryImage: "registry"}},
		// A digest that is not one. Half a digest, an algorithm nothing publishes
		// under and the upper-case form no registry serves would each satisfy a
		// "contains @sha256:" check and none of them addresses content.
		{"truncated digest", "pacto image",
			ComposeOptions{PactoImage: "ghcr.io/x@sha256:abc", RegistryImage: pinnedRegistry}},
		{"upper-case digest", "pacto image",
			ComposeOptions{PactoImage: "ghcr.io/x@sha256:" + strings.ToUpper(sixtyFour), RegistryImage: pinnedRegistry}},
		{"another algorithm", "registry image",
			ComposeOptions{PactoImage: pinnedPacto, RegistryImage: "registry@md5:" + sixtyFour}},
		{"digest with no repository", "registry image",
			ComposeOptions{PactoImage: pinnedPacto, RegistryImage: "@sha256:" + sixtyFour}},
	} {
		switch _, err := OperationalGraph.Compose(tc.opts); {
		case err == nil:
			t.Errorf("%s: the projection accepted %q, so the demo is not pinned", tc.name, tc.opts)
		case !strings.Contains(err.Error(), tc.says):
			t.Errorf("%s: refused with %q, which does not say which image is unpinned", tc.name, err)
		case !strings.Contains(err.Error(), "sha256:"):
			t.Errorf("%s: refused with %q, which does not say what a pinned reference looks like", tc.name, err)
		}
	}
}

// The pinned reference reaches the compose file unchanged, and nothing beside it
// narrows what that reference resolves to.
//
// Both images ship a MULTI-PLATFORM INDEX, and an index digest is the one digest
// form that still lets Docker pick the child matching the host. Rewriting the
// reference — normalising it, splitting the digest off, adding a `platform:` next
// to it — would either break the pin or turn a multi-architecture demo into an
// emulated one. So the projection copies it, and this is the assertion that it
// copies rather than interprets.
func TestCompose_EmitsThePinnedReferencesUnchanged(t *testing.T) {
	f := composeFileOf(t, OperationalGraph)
	want := map[string]string{
		"registry":  composeOpts.RegistryImage,
		"evidence":  composeOpts.PactoImage,
		"seed":      composeOpts.PactoImage,
		"dashboard": composeOpts.PactoImage,
	}
	services, ok := f["services"].(map[string]any)
	if !ok {
		t.Fatal("the projection declares no services")
	}
	if len(services) != len(want) {
		t.Fatalf("the projection runs %v; this checks %v", keysOfAny(services), keysOfAny(map[string]any{
			"registry": nil, "evidence": nil, "seed": nil, "dashboard": nil}))
	}
	for name, img := range want {
		svc := composeSvc(t, f, name)
		if got := svc["image"]; got != img {
			t.Errorf("service %s runs %v, want the pinned reference %q byte for byte", name, got, img)
		}
		if p, pinned := svc["platform"]; pinned {
			t.Errorf("service %s pins platform %v, so the index digest could not resolve to the host's own child", name, p)
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

// Mutable state is in a named volume, and NOTHING is bind-mounted.
//
// A bind mount is a path on the machine that started the demo. There is no such
// path: the artifact is one compose file pulled from a registry by digest, so a
// bind mount would resolve against whatever directory the user happened to be in
// and the demo would come up reading someone else's files, or nothing at all.
// This is the assertion that the projection never regrows the run directory it
// used to have.
func TestCompose_BindMountsNothing(t *testing.T) {
	f := composeFileOf(t, OperationalGraph)
	vols, ok := f["volumes"].(map[string]any)
	if !ok || len(vols) == 0 {
		t.Fatal("the demo declares no named volume, so nothing it writes survives a restart")
	}
	services, _ := f["services"].(map[string]any)
	for name, raw := range services {
		svc, _ := raw.(map[string]any)
		for _, m := range listOf(t, svc, "volumes") {
			source, _, _ := strings.Cut(m, ":")
			if _, declared := vols[source]; !declared {
				t.Errorf("service %s mounts %q, which is not one of this application's named volumes; a published artifact has no host path to bind", name, m)
			}
		}
		// The long form says it outright, so refuse it by type too rather than
		// only by the shape of a short string.
		raws, _ := svc["volumes"].([]any)
		for _, v := range raws {
			if long, isLong := v.(map[string]any); isLong && long["type"] != "volume" {
				t.Errorf("service %s declares a %v mount; only named volumes may appear", name, long["type"])
			}
		}
	}
}

// Every immutable fixture input travels INLINE and is mounted only into the one
// service that reads it.
//
// `content`, never `file`: a `file` config would point at the run directory the
// artifact does not have. Read-only at 0444 — Compose's own default — so the
// demo's own processes cannot rewrite the fixture they are being measured
// against. Split by consumer so the runtime, and not a convention, is what keeps
// the dashboard away from the seed's plan and the seed away from an observation
// export.
func TestCompose_CarriesEveryFixtureInputInline(t *testing.T) {
	f := composeFileOf(t, OperationalGraph)
	configs, ok := f["configs"].(map[string]any)
	if !ok || len(configs) == 0 {
		t.Fatal("the application carries no configs, so the demo has no fixture to read")
	}
	for name, raw := range configs {
		c, _ := raw.(map[string]any)
		if _, external := c["file"]; external {
			t.Errorf("config %s reads a file from the host; a published application has no directory beside it", name)
		}
		if body, _ := c["content"].(string); body == "" {
			t.Errorf("config %s carries no inline content", name)
		}
	}
	mounted := map[string]int{}
	for svcName, raw := range f["services"].(map[string]any) {
		svc, _ := raw.(map[string]any)
		declared, _ := svc["configs"].([]any)
		for i, v := range declared {
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("%s configs[%d] is %T, not the long form that names a target", svcName, i, v)
			}
			source, _ := m["source"].(string)
			if _, declared := configs[source]; !declared {
				t.Errorf("service %s mounts the undeclared config %q", svcName, source)
			}
			target, _ := m["target"].(string)
			if !strings.HasPrefix(target, ComposeArtifactMount+"/") {
				t.Errorf("service %s mounts config %s at %q, outside %s", svcName, source, target, ComposeArtifactMount)
			}
			mounted[source]++
		}
	}
	for name := range configs {
		switch mounted[name] {
		case 1:
		case 0:
			t.Errorf("config %s is carried but never mounted, so it is weight in the artifact nothing reads", name)
		default:
			t.Errorf("config %s is mounted into %d services; each input belongs to the one service that reads it", name, mounted[name])
		}
	}
}

// TestCompose_CarriesTheCanonicalBytes: what travels inside the artifact is the
// scenario's own material, byte for byte. Content that drifted, was hand-written
// into the projection or was swapped for a path to something on a host would
// still be an inline config mounted into the right service — this is what
// compares it to the one canonical source.
func TestCompose_CarriesTheCanonicalBytes(t *testing.T) {
	files, err := OperationalGraph.MaterializeFiles(ComposeDomain)
	if err != nil {
		t.Fatalf("MaterializeFiles: %v", err)
	}
	f := composeFileOf(t, OperationalGraph)
	// Where each config lands is what says which canonical document it is.
	rel := map[string]string{}
	for _, raw := range f["services"].(map[string]any) {
		svc, _ := raw.(map[string]any)
		declared, _ := svc["configs"].([]any)
		for _, v := range declared {
			m, _ := v.(map[string]any)
			source, _ := m["source"].(string)
			target, _ := m["target"].(string)
			rel[source] = strings.TrimPrefix(target, ComposeArtifactMount+"/")
		}
	}
	var checked int
	configs, _ := f["configs"].(map[string]any)
	for name, raw := range configs {
		want, ok := files[rel[name]]
		if !ok { // the plan, the seed script, the evidence payloads and the trace
			continue // exports are generated per run, not materialized documents
		}
		c, _ := raw.(map[string]any)
		if got, _ := c["content"].(string); got != literal(want) {
			t.Errorf("config %s is not the canonical %s the scenario materializes", name, rel[name])
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no config was matched to a canonical document, so this compared nothing")
	}
}

// The artifact says which release it is and which Compose can run it.
//
// It carries no README and no second layer to put either in — the OCI artifact
// type for a Compose application is the compose file — so this is an `x-`
// extension, which survives publication verbatim and is what a user with only a
// digest gets back from `docker compose -f oci://…@sha256:… config`. The version
// also keeps two releases two artifacts: byte-identical compose files would be
// one digest and one demo.
func TestCompose_SaysWhatItIsAndWhatCanRunIt(t *testing.T) {
	ext, ok := composeFileOf(t, OperationalGraph)["x-pacto-demo"].(map[string]any)
	if !ok {
		t.Fatal("the application says nothing about itself, so a user holding only a digest cannot tell what they pulled")
	}
	for key, want := range map[string]any{
		"version":                 composeOpts.Version,
		"source":                  ComposeSourceURL,
		"documentation":           ComposeDocsURL,
		"minimum-compose-version": ComposeMinVersion,
	} {
		if got := ext[key]; got != want {
			t.Errorf("x-pacto-demo.%s is %v, want %q", key, got, want)
		}
	}
	// And it is REQUIRED, because the alternative is a release that publishes an
	// artifact identical to the last one and a ledger that records two.
	if _, err := OperationalGraph.Compose(ComposeOptions{
		PactoImage: composeOpts.PactoImage, RegistryImage: composeOpts.RegistryImage,
	}); err == nil || !strings.Contains(err.Error(), "version") {
		t.Errorf("the projection built an unversioned artifact: %v", err)
	}
}
