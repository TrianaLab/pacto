package scenario

import (
	"bytes"
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file is the COMPOSE surface: the same scenario, projected onto four
// containers instead of a cluster.
//
// Everything the two surfaces share is derived from the same fields — which
// services publish revisions, what the observation source is called, who signs
// the evidence — so the demo a user runs on their laptop and the fixture CI
// proves in Kind cannot describe different systems. What Compose genuinely
// cannot do is DECLARED, once, as a missing Capability (see surface.go) rather
// than quietly omitted: there is no controller here, so nothing reconciles a
// Pacto CR into an operational target, and the Product gate subtracts exactly
// those facts and says so.
//
// The projection renders the file through Go structs, like PactoCRs, so every
// value is escaped by the YAML encoder instead of interpolated into text. The
// irreducibly imperative part — push these bundles, sign these envelopes — is
// NOT generated: it is a static script that reads the same tab-delimited Plan
// the Kind harness reads.
//
// The artifact IS this file. Docker Compose owns the OCI artifact type for a
// Compose application (`docker compose publish`, `docker compose -f oci://…`),
// and that type carries a compose file and nothing else — no directory, no
// second layer, no README. So every immutable fixture input the demo reads
// travels INLINE, as a Compose `configs` entry mounted read-only into the one
// service that reads it. Nothing is bind-mounted, and there is no run directory
// to be in: a user runs the artifact straight out of the registry by digest.
//
// Three things stay outside the artifact, because an artifact is immutable and
// they are not: the keypair (minted at run time into a volume), the registry's
// content (pushed at run time by the seed) and the host ports (environment
// overrides over the defaults below).

// The addresses inside the Compose network. They are part of the projection
// rather than runtime inputs because the demo brings its own registry up under a
// name it chooses: unlike Kind, where the harness discovers whatever port the
// host assigned, nothing here can be different from run to run.
const (
	// ComposeRegistryHost is the in-network address of the registry the demo starts.
	ComposeRegistryHost = "registry:5000"
	// ComposeDomain is the OCI domain the demo's bundles are published under.
	ComposeDomain = ComposeRegistryHost + "/demo"
	// ComposeArtifactMount is the directory every inline fixture config is targeted
	// into, read-only. It is the only path the demo reads fixture data from, which
	// is what makes the published compose file — and nothing else — the whole input.
	ComposeArtifactMount = "/demo"
	// ComposeEvidenceURL is the in-network address of the Evidence Server.
	ComposeEvidenceURL = "http://evidence:8686"
	// ComposePlanFile is the execution plan inside the artifact, in Plan's format.
	ComposePlanFile = ComposeArtifactMount + "/plan.tsv"
	// ComposeSeedScript is the static script the one-shot seed runs.
	ComposeSeedScript = ComposeArtifactMount + "/seed.sh"
)

// ComposeMinVersion is the oldest Docker Compose that owns this artifact type:
// 2.34.0 added `docker compose publish` and `-f oci://…`. Declared once, here,
// because the projection, the release unit, CI and the documented user journey
// all have to mean the same floor — and a user on an older Compose sees a file
// it will not load rather than an actionable version error.
const ComposeMinVersion = "2.34.0"

// The provenance a Compose OCI artifact CAN carry. The artifact type is a
// compose file, so there is no layer to put a README in; `x-` extensions are the
// native way to attach metadata, and they survive publication verbatim, so
// `docker compose -f oci://…@sha256:… config` shows a user what they pulled and
// where the instructions are.
const (
	// ComposeSourceURL is the project the artifact is built from.
	ComposeSourceURL = "https://github.com/trianalab/pacto"
	// ComposeDocsURL is where the authoritative instructions live. The artifact
	// cannot carry them: see the extension's own comment.
	ComposeDocsURL = ComposeSourceURL + "/blob/main/docs/examples/compose-demo.md"
)

// seedScript is the artifact's one static document: the imperative half of the
// demo, embedded so it travels inline instead of being read off a disk that,
// after publication, no longer exists.
//
//go:embed seed.sh
var seedScript string

// The container-side paths of the state volume. It is mounted at the image's
// HOME, which exists in the image and is owned by the non-root user: a named
// volume at a path the image does NOT have is created root-owned, and the
// dashboard could not write its OCI cache into it.
const (
	composeStateMount = "/home/pacto"
	composeTrustDir   = composeStateMount + "/trust"
	composeKeyDir     = composeStateMount + "/keys"
)

const (
	composeStateVolume    = "pacto-demo-state"
	composeRegistryVolume = "pacto-demo-registry"
	// The container ports. Fixed, because nothing else is listening in there.
	composeDashboardPort = 3000
	composeEvidencePort  = 8686
	composeRegistryPort  = 5000
)

// ComposePort is one published port: which service listens, and the environment
// variable that moves it on the host.
type ComposePort struct {
	Service   string
	Env       string
	Default   int
	Container int
}

// ComposePorts is every port the demo publishes.
//
// One declaration, two consumers: the `ports` entries and the acceptance harness
// that has to know where to reach the dashboard. The defaults avoid the ports the
// repository's other harnesses bind, so a demo and a test run can coexist on one
// machine — and each is overridable by its variable, which is how two versions of
// the artifact run side by side without colliding.
func ComposePorts() []ComposePort {
	return []ComposePort{
		{Service: "dashboard", Env: "PACTO_DEMO_DASHBOARD_PORT", Default: 8080, Container: composeDashboardPort},
		{Service: "evidence", Env: "PACTO_DEMO_EVIDENCE_PORT", Default: 8686, Container: composeEvidencePort},
		{Service: "registry", Env: "PACTO_DEMO_REGISTRY_PORT", Default: 5051, Container: composeRegistryPort},
	}
}

// ComposeDefaultRegistryImage is the OCI registry the demo starts, pinned to the
// MULTI-PLATFORM INDEX digest of zot-minimal.
//
// zot, not CNCF distribution, because the demo's registry is also its EVIDENCE
// STORE: accepted evidence is published as an OCI 1.1 referrer of the contract
// revision it reports on, and Pacto refuses oras-go's legacy referrers-tag
// fallback. `registry:2` and `registry:3` implement no referrers endpoint at
// all, so the Evidence Server would never pass its own readiness probe and the
// demo would come up with a permanently unhealthy container. zot-minimal keeps
// the same port and the same /var/lib/registry, and its "minimal" build carries
// no CVE-database downloader, so the demo needs nothing from the network once
// its images are pulled.
//
// The index, not one of its children. An index digest still lets Docker resolve
// the child matching the host, so the demo stays native on amd64 and arm64 alike
// with no `platform:` anywhere; a per-architecture MANIFEST digest would pin one
// of them and emulate it everywhere else. The two are indistinguishable as
// strings, which is exactly how they get confused — this repository has already
// paid for that once, in the `kind load` failure documented in
// docs/maintainers/testing.md, where sha256:46faa9a1… is the amd64 CHILD of this
// same image. The local acceptance re-derives it: it asserts the pulled registry
// image is the host's own architecture, which a child digest could not be.
//
// Refreshing it is `crane digest ghcr.io/project-zot/zot-minimal:<tag>` —
// deliberately a human act, since a new digest is a new demo and the artifact
// says which one it ran.
const ComposeDefaultRegistryImage = "ghcr.io/project-zot/zot-minimal:v2.1.20@sha256:73f26433b341f4a319963f7c5e169858663a10565e4037e71605737daee202ee"

// ComposeOptions are the values the Compose projection cannot derive: the images
// it runs. They are required rather than defaulted because the whole point of
// the distributed artifact is that it is pinned — a default would produce a demo
// that meant whatever `latest` meant on the day it was started.
//
// Both must be DIGEST-QUALIFIED. Everything else about the artifact is already
// immutable — it is published, pulled and documented by digest — so a tag left
// in here is the one way the same demo artifact can execute different bytes
// tomorrow than it did today, silently, with its own digest unchanged.
type ComposeOptions struct {
	// PactoImage runs the dashboard, the Evidence Server and the seed.
	PactoImage string
	// RegistryImage runs the OCI registry the demo publishes into.
	RegistryImage string
	// Version is the release this artifact was built for. It is part of the
	// application rather than a label on it: the artifact carries no file a user
	// could read a version out of, and two releases whose compose files were
	// byte-identical would be one artifact under one digest.
	Version string
}

// Compose renders the Docker Compose projection of the scenario.
func (s Scenario) Compose(opts ComposeOptions) ([]byte, error) {
	if opts.PactoImage == "" || opts.RegistryImage == "" {
		return nil, fmt.Errorf("scenario %s: the Compose projection needs both a pacto image and a registry image; a default would unpin the demo", s.Name)
	}
	if err := checkPinnedImage("pacto image", opts.PactoImage); err != nil {
		return nil, fmt.Errorf("scenario %s: %w", s.Name, err)
	}
	if err := checkPinnedImage("registry image", opts.RegistryImage); err != nil {
		return nil, fmt.Errorf("scenario %s: %w", s.Name, err)
	}
	if opts.Version == "" {
		return nil, fmt.Errorf("scenario %s: the Compose projection needs the version it is being built for; the artifact carries no other file to record it in", s.Name)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	signer, err := s.signer()
	if err != nil {
		return nil, err
	}
	if signer == (Signer{}) {
		return nil, fmt.Errorf("scenario %s: no evidence is declared, so the demo would start an Evidence Server nothing signs for", s.Name)
	}
	dash, err := s.composeDashboardArgs()
	if err != nil {
		return nil, err
	}
	for _, v := range []string{opts.PactoImage, opts.RegistryImage, opts.Version, signer.Producer, signer.KeyID} {
		if err := checkComposeValue(v); err != nil {
			return nil, fmt.Errorf("scenario %s: %w", s.Name, err)
		}
	}
	seedInputs, dashInputs, subjects, err := s.composeInputs()
	if err != nil {
		return nil, err
	}
	for _, subj := range subjects {
		if err := checkComposeValue(subj); err != nil {
			return nil, fmt.Errorf("scenario %s: evidence subject: %w", s.Name, err)
		}
	}

	f := composeFile{Extension: composeExtension{
		Version:        opts.Version,
		Source:         ComposeSourceURL,
		Documentation:  ComposeDocsURL,
		MinimumCompose: ComposeMinVersion,
	}, Services: composeServices{
		Registry: composeService{
			Image:   opts.RegistryImage,
			Restart: "unless-stopped",
			Ports:   []composePort{portMapping("registry")},
			Volumes: []string{composeRegistryVolume + ":/var/lib/registry"},
		},
		Evidence: composeService{
			Image:      opts.PactoImage,
			Restart:    "unless-stopped",
			Entrypoint: []string{"/bin/sh", "-euc"},
			Command:    []string{evidenceScript(signer, subjects)},
			// Ingestion RESOLVES each envelope's ContractRef before accepting it, so
			// the server reaches the registry itself; without this it answers 502
			// contract_resolution_failed on a plain-HTTP demo registry.
			Environment: map[string]string{"PACTO_INSECURE_REGISTRIES": ComposeRegistryHost},
			Ports:       []composePort{portMapping("evidence")},
			Volumes:     []string{composeStateVolume + ":" + composeStateMount},
			// The image's baked healthcheck probes the dashboard, which is not what
			// runs here; left inherited it would never pass and `up --wait` would sit
			// until it timed out. Readiness is the server's own: 503 until every
			// configured subject resolves and answers native Referrers discovery,
			// 200 after.
			Healthcheck: &composeHealth{
				Test:        []string{"CMD-SHELL", "wget -q --spider http://127.0.0.1:" + strconv.Itoa(composeEvidencePort) + "/api/evidence/v1/ready || exit 1"},
				Interval:    "3s",
				Timeout:     "3s",
				Retries:     20,
				StartPeriod: "5s",
			},
		},
		Seed: composeService{
			Image:      opts.PactoImage,
			Restart:    "no",
			Entrypoint: []string{"/bin/sh", ComposeSeedScript},
			// PUBLISH_TO, not REGISTRY: release/scripts/publish-demo-bundles.sh already
			// owns PACTO_DEMO_REGISTRY for a different thing, and
			// PACTO_DEMO_REGISTRY_PORT is a third. Three near-identical names for three
			// unrelated values is how somebody sets the wrong one.
			Environment: map[string]string{
				"PACTO_INSECURE_REGISTRIES": ComposeRegistryHost,
				"PACTO_DEMO_PUBLISH_TO":     ComposeDomain,
				"PACTO_DEMO_EVIDENCE_URL":   ComposeEvidenceURL,
				"PACTO_DEMO_PLAN":           ComposePlanFile,
				"PACTO_DEMO_KEYS":           composeKeyDir,
			},
			DependsOn: map[string]composeDep{
				// The registry needs no healthcheck of its own: the seed's first push
				// retries, which is the same observation by a shorter route.
				"registry": {Condition: "service_started"},
				"evidence": {Condition: "service_healthy"},
			},
			Volumes: []string{composeStateVolume + ":" + composeStateMount},
			Configs: mounts(seedInputs),
			// A one-shot container that exits 0 is not unhealthy, but the image's
			// inherited probe would call it that and block everything downstream.
			Healthcheck: &composeHealth{Disable: true},
		},
		Dashboard: composeService{
			Image:   opts.PactoImage,
			Restart: "unless-stopped",
			Command: dash,
			Environment: map[string]string{
				"PACTO_INSECURE_REGISTRIES": ComposeRegistryHost,
				"PACTO_EVIDENCE_SOURCE_URL": ComposeEvidenceURL,
			},
			// Only after the seed has SUCCEEDED: the dashboard's first snapshot is the
			// one a user sees, and a snapshot taken against an empty registry would
			// show an empty fleet until the next refresh.
			DependsOn: map[string]composeDep{"seed": {Condition: "service_completed_successfully"}},
			Ports:     []composePort{portMapping("dashboard")},
			Volumes:   []string{composeStateVolume + ":" + composeStateMount},
			Configs:   mounts(dashInputs),
		},
	}, Configs: contents(seedInputs, dashInputs)}

	var buf bytes.Buffer
	buf.WriteString("# Generated by tests/acceptance/scenario. Do not edit: the Pacto demo is a\n" +
		"# projection of one canonical scenario, shared with the Kubernetes surface.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// composeInput is one immutable fixture document the artifact carries INLINE:
// the name Compose knows it by, the path its one consumer reads it at, and the
// bytes.
type composeInput struct {
	name, target, content string
}

// composeInputs is every fixture document the demo reads, split by the ONE
// service that reads it.
//
// This is the whole reason there is no run directory. The Kubernetes surface
// hands these to a cluster as ConfigMaps and the harness writes them to disk;
// here they are `configs` with inline content, because the published artifact is
// a compose file and a compose file is all a user has. Same projection, same
// canonical scenario, three transports.
//
// Split by consumer rather than mounted everywhere: the dashboard has no
// business reading the seed's private key plan, and the seed has no business
// reading an observation export. Compose only creates the files a service
// declares, so the split is enforced by the runtime and not by convention.
func (s Scenario) composeInputs() (seed, dashboard []composeInput, subjects []string, err error) {
	files, err := s.MaterializeFiles(ComposeDomain)
	if err != nil {
		return nil, nil, nil, err
	}
	plan, err := s.Plan(ComposeArtifactMount)
	if err != nil {
		return nil, nil, nil, err
	}
	// No registry exists yet, so the digests come from the bytes above. The seed
	// re-checks each one against what it actually publishes.
	digests, err := s.Digests(files)
	if err != nil {
		return nil, nil, nil, err
	}
	payloads, err := s.EvidencePayloads(ComposeArtifactMount, ComposeDomain, digests)
	if err != nil {
		return nil, nil, nil, err
	}
	seedFiles := map[string]string{"plan.tsv": string(plan), "seed.sh": seedScript}
	for rel, body := range files {
		seedFiles[rel] = body
	}
	for abs, body := range payloads {
		seedFiles[strings.TrimPrefix(abs, ComposeArtifactMount+"/")] = string(body)
	}
	dashFiles := map[string]string{}
	for _, src := range s.Sources {
		if src.Kind != SourceObservation {
			continue
		}
		export, err := s.TraceExport(src.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		dashFiles[src.ID+".json"] = string(export)
	}

	// One namespace for both consumers, so a bundle document and an export that
	// projected onto the same config name would be caught here rather than
	// silently overwrite each other in the published application.
	taken := map[string]string{}
	convert := func(m map[string]string) ([]composeInput, error) {
		out := make([]composeInput, 0, len(m))
		for _, rel := range sortedKeys(m) {
			name := strings.ReplaceAll(rel, "/", "_")
			if prior, dup := taken[name]; dup {
				return nil, fmt.Errorf("scenario %s: %q and %q both project onto the Compose config name %q", s.Name, prior, rel, name)
			}
			taken[name] = rel
			if err := checkConfigName(name); err != nil {
				return nil, fmt.Errorf("scenario %s: %q: %w", s.Name, rel, err)
			}
			out = append(out, composeInput{name: name, target: ComposeArtifactMount + "/" + rel, content: literal(m[rel])})
		}
		return out, nil
	}
	if seed, err = convert(seedFiles); err != nil {
		return nil, nil, nil, err
	}
	if dashboard, err = convert(dashFiles); err != nil {
		return nil, nil, nil, err
	}
	// The subjects come from the SAME digests the payloads were built against, so
	// the server is configured with exactly the revisions the envelopes name — it
	// cannot be told to store evidence anywhere else.
	if subjects, err = s.EvidenceSubjects(ComposeDomain, digests); err != nil {
		return nil, nil, nil, err
	}
	return seed, dashboard, subjects, nil
}

// literal is inline config content that survives Compose's interpolation pass.
//
// Compose interpolates a config's `content` exactly as it interpolates the rest
// of the file, so `$WORK` in the seed script becomes the empty string and the
// script silently runs against nothing. `$$` is the spec's escape and expands
// back to a single `$` when the config is materialized, so what the container
// reads is byte-for-byte what the projection produced — which is also what makes
// a bundle's published digest match the one computed here.
//
// The ports keep their single `$`: `published: ${PACTO_DEMO_…}` is the one place
// the artifact WANTS the user's environment to reach in.
func literal(body string) string { return strings.ReplaceAll(body, "$", "$$") }

// checkConfigName refuses a name Compose would not accept as a top-level key.
// The projection derives names from artifact paths, so this is a projection bug
// caught at build time rather than a load failure on a user's laptop.
func checkConfigName(name string) error {
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("._-", r):
		default:
			return fmt.Errorf("projects onto the Compose config name %q, which contains %q", name, string(r))
		}
	}
	return nil
}

// mounts is the service side of the inputs: read-only at the declared path, at
// the default 0444 the Compose spec gives a config, which is what makes them
// readable by the image's non-root user without a chmod pass over a directory.
func mounts(in []composeInput) []composeServiceConfig {
	out := make([]composeServiceConfig, 0, len(in))
	for _, i := range in {
		out = append(out, composeServiceConfig{Source: i.name, Target: i.target})
	}
	return out
}

// contents is the top-level side: the bytes themselves, once.
func contents(groups ...[]composeInput) map[string]composeConfig {
	out := map[string]composeConfig{}
	for _, g := range groups {
		for _, i := range g {
			out[i.name] = composeConfig{Content: i.content}
		}
	}
	return out
}

// composeDashboardArgs is what the dashboard is told to read.
//
// The OCI sources are every service that is NOT EvidenceOnly — the same
// predicate the Product gate uses to decide which services owe a canonical
// revision, so the dashboard is pointed at exactly what will be required of it.
// Deriving the list from "runs a workload" instead would agree with this fixture
// by accident and drop a published service the moment one stopped running.
//
// The REPOSITORY and then EVERY REVISION in it, because the two say different
// things. The repository is what the fleet's registry source resolves to the
// highest published version, and it is the only form the legacy service list can
// enumerate tags from; a version is a published artifact this fixture expects to
// find whether or not it is the newest. Kubernetes gets both for free — the
// operator reports each Pacto CR's declared ref alongside the digest it resolved
// to — so naming only the repository here would leave every superseded revision
// known to the disk cache alone, which is a source disagreeing with the registry
// about what was published rather than a shorter fixture.
func (s Scenario) composeDashboardArgs() ([]string, error) {
	args := []string{"dashboard"}
	for _, svc := range s.Services {
		if svc.EvidenceOnly {
			continue
		}
		repo := "oci://" + ComposeDomain + "/" + svc.Repo
		args = append(args, repo)
		for _, rev := range svc.Revisions {
			args = append(args, repo+":"+rev.Version)
		}
	}
	if len(args) == 1 {
		return nil, fmt.Errorf("scenario %s: no service publishes an OCI revision, so the dashboard would show an empty fleet", s.Name)
	}
	for _, src := range s.Sources {
		if src.Kind != SourceObservation {
			continue
		}
		args = append(args, "--trace-source", src.ID+"="+ComposeArtifactMount+"/"+src.ID+".json")
	}
	args = append(args, "--host", "0.0.0.0", "--port", strconv.Itoa(composeDashboardPort))
	for _, a := range args {
		if err := checkComposeValue(a); err != nil {
			return nil, fmt.Errorf("scenario %s: dashboard argument: %w", s.Name, err)
		}
	}
	return args, nil
}

// evidenceScript starts the Evidence Server on the identity the scenario
// declares, minting its keypair on first run.
//
// The private key is MOVED out of the trust directory it is written into: the
// trust loader reads every *.pub there, and leaving the key beside them would put
// signing material in the directory whose job is to hold only public keys. The
// guard makes a restart reuse the identity it already published, so an envelope
// signed before the restart still verifies after it.
func evidenceScript(signer Signer, subjects []string) string {
	serve := "exec pacto evidence serve" +
		" --listen-address 0.0.0.0:" + strconv.Itoa(composeEvidencePort) +
		" --trust " + composeTrustDir
	for _, subj := range subjects {
		serve += " --subject " + subj
	}
	return strings.Join([]string{
		"mkdir -p " + composeTrustDir + " " + composeKeyDir,
		"if [ ! -f " + composeKeyDir + "/" + signer.KeyID + ".key ]; then",
		"  pacto evidence keygen --out " + composeTrustDir +
			" --key-id " + signer.KeyID + " --producer " + signer.Producer,
		"  mv " + composeTrustDir + "/" + signer.KeyID + ".key " + composeKeyDir + "/" + signer.KeyID + ".key",
		"fi",
		serve + " --producer " + signer.Producer,
	}, "\n") + "\n"
}

// portMapping is a published port as Compose reads it: the default, overridable
// by the declared variable, in front of the fixed container port.
//
// The LONG form. `docker compose publish` refuses the `host:container` short
// string, and the long form says what the short one only implies — which side is
// the container's. The interpolation survives publication byte-for-byte, so the
// variable still moves the port on a host that has something on 8080, which is
// what lets two versions of the artifact run at once.
func portMapping(service string) composePort {
	for _, p := range ComposePorts() {
		if p.Service == service {
			return composePort{
				Target:    p.Container,
				Published: "${" + p.Env + ":-" + strconv.Itoa(p.Default) + "}",
				Protocol:  "tcp",
				Mode:      "ingress",
			}
		}
	}
	panic("no port declared for compose service " + service)
}

// checkPinnedImage refuses an image reference a tag could move.
//
// Fail-closed and by NAME: the caller is a release step or a harness several
// files away, and "the demo came up running last week's dashboard" is not a
// failure anything downstream can see. `repo:tag@sha256:…` is accepted alongside
// the bare digest form — the tag is then documentation and Docker resolves the
// digest — but the digest itself has to be one: 64 LOWER-CASE hex characters
// under sha256, which is the only spelling a registry serves. Half a digest and
// the upper-case form both satisfy "contains @sha256:" and neither addresses
// content.
func checkPinnedImage(what, ref string) error {
	name, digest, found := strings.Cut(ref, "@")
	hex, isSHA256 := strings.CutPrefix(digest, "sha256:")
	switch {
	case !found:
		return fmt.Errorf("the %s %q is not pinned: it must end in @sha256:<64 hex>, or one immutable demo artifact runs whatever the tag points at on the day it is started", what, ref)
	case name == "":
		return fmt.Errorf("the %s %q names no repository before its @sha256: digest", what, ref)
	case !isSHA256 || len(hex) != 64:
		return fmt.Errorf("the %s %q is pinned to %q, which is not an @sha256: digest of 64 hex characters", what, ref, digest)
	}
	if strings.Trim(hex, "0123456789abcdef") != "" {
		return fmt.Errorf("the %s %q is pinned to %q, which is not lower-case hex, so no registry would serve it as an @sha256: digest", what, ref, digest)
	}
	return nil
}

// checkComposeValue refuses a scenario value that could forge a command.
//
// Values reach a generated /bin/sh script and an exec-form argument list, so the
// safe set is what an OCI reference, a source id and a producer id are made of.
// Rejecting by name beats escaping: an escaped shell metacharacter in a producer
// id would produce a demo that started and then failed at ingestion, which is
// harder to read than a refusal that says which value is wrong.
func checkComposeValue(v string) error {
	if v == "" {
		return fmt.Errorf("is empty")
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("._:/@=+-", r):
		default:
			return fmt.Errorf("%q contains %q, which a generated command could not carry safely", v, string(r))
		}
	}
	return nil
}

// The Compose file shape. Services are named fields rather than a map so the
// generated file reads in startup order; there are four of them because the demo
// runs four containers, not because the scenario declares any.
//
// No `name:`. There is no run directory to derive one from either, so the
// project name is the user's `-p`, always — which is what the documented journey
// says and what makes two pulled versions two independent demos. A name pinned
// here would silently hand a freshly pulled artifact the previous version's
// registry and evidence volumes, and "upgrade" would mean "run the new demo over
// the old demo's state"; worse, it would collide across USERS of the same
// artifact on one machine, which a run directory at least could not do.
type composeFile struct {
	Extension composeExtension         `yaml:"x-pacto-demo"`
	Services  composeServices          `yaml:"services"`
	Configs   map[string]composeConfig `yaml:"configs"`
	Volumes   composeVolumes           `yaml:"volumes"`
}

// composeExtension is what an application-type OCI artifact can say about
// itself.
//
// The artifact carries a compose file and nothing else — no layer to put a
// README in — so the instructions live in the repository's documentation and
// this points at them. `x-` keys survive `docker compose publish` verbatim and
// are visible in `docker compose -f oci://…@sha256:… config`, so a user who has
// only a digest can still find out what they pulled. That is a smaller claim
// than a README and an honest one: nothing here pretends the application exposes
// a file Compose cannot hand back.
type composeExtension struct {
	Version        string `yaml:"version"`
	Source         string `yaml:"source"`
	Documentation  string `yaml:"documentation"`
	MinimumCompose string `yaml:"minimum-compose-version"`
}

// composeConfig is one inline fixture document. `content`, never `file`: a
// published application has no directory beside it to read a file from.
type composeConfig struct {
	Content string `yaml:"content"`
}

// composeServiceConfig mounts one of them into the service that reads it.
type composeServiceConfig struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

// composePort is a published port in the long form `docker compose publish`
// accepts.
type composePort struct {
	Target    int    `yaml:"target"`
	Published string `yaml:"published"`
	Protocol  string `yaml:"protocol"`
	Mode      string `yaml:"mode"`
}

type composeServices struct {
	Registry  composeService `yaml:"registry"`
	Evidence  composeService `yaml:"evidence"`
	Seed      composeService `yaml:"seed"`
	Dashboard composeService `yaml:"dashboard"`
}

type composeService struct {
	Image       string                 `yaml:"image"`
	Restart     string                 `yaml:"restart,omitempty"`
	DependsOn   map[string]composeDep  `yaml:"depends_on,omitempty"`
	Entrypoint  []string               `yaml:"entrypoint,omitempty"`
	Command     []string               `yaml:"command,omitempty"`
	Environment map[string]string      `yaml:"environment,omitempty"`
	Ports       []composePort          `yaml:"ports,omitempty"`
	Volumes     []string               `yaml:"volumes,omitempty"`
	Configs     []composeServiceConfig `yaml:"configs,omitempty"`
	Healthcheck *composeHealth         `yaml:"healthcheck,omitempty"`
}

type composeDep struct {
	Condition string `yaml:"condition"`
}

type composeHealth struct {
	Disable     bool     `yaml:"disable,omitempty"`
	Test        []string `yaml:"test,omitempty"`
	Interval    string   `yaml:"interval,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
	Retries     int      `yaml:"retries,omitempty"`
	StartPeriod string   `yaml:"start_period,omitempty"`
}

// composeVolumes declares the two named volumes with no options, which is what
// `docker compose down -v` removes and a plain `down` keeps.
type composeVolumes struct {
	State    struct{} `yaml:"pacto-demo-state"`
	Registry struct{} `yaml:"pacto-demo-registry"`
}
