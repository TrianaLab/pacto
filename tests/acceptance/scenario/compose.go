package scenario

import (
	"bytes"
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
// NOT generated: it is a static script in the artifact that reads the same
// tab-delimited Plan the Kind harness reads.
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
	// ComposeArtifactMount is where the pulled artifact is mounted, read-only. It is
	// the only path the demo reads fixture data from, which is what makes the run
	// directory — and nothing else — the whole input.
	ComposeArtifactMount = "/demo"
	// ComposeEvidenceURL is the in-network address of the Evidence Server.
	ComposeEvidenceURL = "http://evidence:8686"
	// ComposePlanFile is the execution plan inside the artifact, in Plan's format.
	ComposePlanFile = ComposeArtifactMount + "/plan.tsv"
	// ComposeSeedScript is the static script the one-shot seed runs.
	ComposeSeedScript = ComposeArtifactMount + "/seed.sh"
)

// The container-side paths of the state volume. It is mounted at the image's
// HOME, which exists in the image and is owned by the non-root user: a named
// volume at a path the image does NOT have is created root-owned, and the
// dashboard could not write its OCI cache into it.
const (
	composeStateMount = "/home/pacto"
	composeTrustDir   = composeStateMount + "/trust"
	composeKeyDir     = composeStateMount + "/keys"
	composeStoreDir   = composeStateMount + "/evidence"
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
// One declaration, three consumers: the `ports` entries, the .env file shipped
// beside the compose file, and the acceptance harness that has to know where to
// reach the dashboard. The defaults avoid the ports the repository's other
// harnesses bind, so a demo and a test run can coexist on one machine.
func ComposePorts() []ComposePort {
	return []ComposePort{
		{Service: "dashboard", Env: "PACTO_DEMO_DASHBOARD_PORT", Default: 8080, Container: composeDashboardPort},
		{Service: "evidence", Env: "PACTO_DEMO_EVIDENCE_PORT", Default: 8686, Container: composeEvidencePort},
		{Service: "registry", Env: "PACTO_DEMO_REGISTRY_PORT", Default: 5051, Container: composeRegistryPort},
	}
}

// ComposeEnv is the .env file shipped in the artifact: the same defaults the
// compose file falls back to, written where a user can edit them. Compose reads
// .env from the run directory, so editing the file and exporting the variable
// have to mean the same thing — a test proves the two agree.
func ComposeEnv() []byte {
	var b strings.Builder
	b.WriteString("# Host ports the Pacto demo publishes. Change one if it is taken.\n")
	for _, p := range ComposePorts() {
		b.WriteString(p.Env + "=" + strconv.Itoa(p.Default) + "\n")
	}
	return []byte(b.String())
}

// ComposeOptions are the values the Compose projection cannot derive: the images
// it runs. They are required rather than defaulted because the whole point of
// the distributed artifact is that it is pinned — a default would produce a demo
// that meant whatever `latest` meant on the day it was started.
type ComposeOptions struct {
	// PactoImage runs the dashboard, the Evidence Server and the seed.
	PactoImage string
	// RegistryImage runs the OCI registry the demo publishes into.
	RegistryImage string
}

// Compose renders the Docker Compose projection of the scenario.
func (s Scenario) Compose(opts ComposeOptions) ([]byte, error) {
	if opts.PactoImage == "" || opts.RegistryImage == "" {
		return nil, fmt.Errorf("scenario %s: the Compose projection needs both a pacto image and a registry image; a default would unpin the demo", s.Name)
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
	for _, v := range []string{opts.PactoImage, opts.RegistryImage, signer.Producer, signer.KeyID} {
		if err := checkComposeValue(v); err != nil {
			return nil, fmt.Errorf("scenario %s: %w", s.Name, err)
		}
	}

	f := composeFile{Services: composeServices{
		Registry: composeService{
			Image:   opts.RegistryImage,
			Restart: "unless-stopped",
			Ports:   []string{portMapping("registry")},
			Volumes: []string{composeRegistryVolume + ":/var/lib/registry"},
		},
		Evidence: composeService{
			Image:      opts.PactoImage,
			Restart:    "unless-stopped",
			Entrypoint: []string{"/bin/sh", "-euc"},
			Command:    []string{evidenceScript(signer)},
			// Ingestion RESOLVES each envelope's ContractRef before accepting it, so
			// the server reaches the registry itself; without this it answers 502
			// contract_resolution_failed on a plain-HTTP demo registry.
			Environment: map[string]string{"PACTO_INSECURE_REGISTRIES": ComposeRegistryHost},
			Ports:       []string{portMapping("evidence")},
			Volumes:     []string{composeStateVolume + ":" + composeStateMount},
			// The image's baked healthcheck probes the dashboard, which is not what
			// runs here; left inherited it would never pass and `up --wait` would sit
			// until it timed out. Readiness is the server's own: 503 until it has
			// recovered its store, 200 after.
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
			Volumes: []string{
				composeStateVolume + ":" + composeStateMount,
				"." + ":" + ComposeArtifactMount + ":ro",
			},
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
			Ports:     []string{portMapping("dashboard")},
			Volumes: []string{
				composeStateVolume + ":" + composeStateMount,
				"." + ":" + ComposeArtifactMount + ":ro",
			},
		},
	}}

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
func evidenceScript(signer Signer) string {
	return strings.Join([]string{
		"mkdir -p " + composeTrustDir + " " + composeKeyDir + " " + composeStoreDir,
		"if [ ! -f " + composeKeyDir + "/" + signer.KeyID + ".key ]; then",
		"  pacto evidence keygen --out " + composeTrustDir +
			" --key-id " + signer.KeyID + " --producer " + signer.Producer,
		"  mv " + composeTrustDir + "/" + signer.KeyID + ".key " + composeKeyDir + "/" + signer.KeyID + ".key",
		"fi",
		"exec pacto evidence serve" +
			" --listen-address 0.0.0.0:" + strconv.Itoa(composeEvidencePort) +
			" --trust " + composeTrustDir +
			" --bucket-url file://" + composeStoreDir +
			" --producer " + signer.Producer,
	}, "\n") + "\n"
}

// portMapping is a published port as Compose reads it: the default, overridable
// by the declared variable, in front of the fixed container port.
func portMapping(service string) string {
	for _, p := range ComposePorts() {
		if p.Service == service {
			return "${" + p.Env + ":-" + strconv.Itoa(p.Default) + "}:" + strconv.Itoa(p.Container)
		}
	}
	panic("no port declared for compose service " + service)
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
// No `name:`. Compose then takes the project name from the RUN DIRECTORY, which
// is what makes two pulled versions two independent demos: pinning a name here
// would silently hand a freshly pulled artifact the previous version's registry
// and evidence volumes, and "upgrade" would mean "run the new demo over the old
// demo's state". The cost is that the volume names carry the directory as a
// prefix, which the artifact's own README says.
type composeFile struct {
	Services composeServices `yaml:"services"`
	Volumes  composeVolumes  `yaml:"volumes"`
}

type composeServices struct {
	Registry  composeService `yaml:"registry"`
	Evidence  composeService `yaml:"evidence"`
	Seed      composeService `yaml:"seed"`
	Dashboard composeService `yaml:"dashboard"`
}

type composeService struct {
	Image       string                `yaml:"image"`
	Restart     string                `yaml:"restart,omitempty"`
	DependsOn   map[string]composeDep `yaml:"depends_on,omitempty"`
	Entrypoint  []string              `yaml:"entrypoint,omitempty"`
	Command     []string              `yaml:"command,omitempty"`
	Environment map[string]string     `yaml:"environment,omitempty"`
	Ports       []string              `yaml:"ports,omitempty"`
	Volumes     []string              `yaml:"volumes,omitempty"`
	Healthcheck *composeHealth        `yaml:"healthcheck,omitempty"`
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
