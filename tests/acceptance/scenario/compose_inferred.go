package scenario

import (
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// The inferred contracts the demo publishes: what an installation's operator
// publishes once the chart's registry value is set, here by `pacto fleet
// publish-inferred`, because Compose runs no operator.
const (
	// ComposeInferredRepository is stripe's inferred contract in the demo registry.
	ComposeInferredRepository = ComposeDomain + "/" + contract.InferredRepository + "/stripe"
	// ComposeInferredConsumer is the Compose-only contract that uses stripe.
	ComposeInferredConsumer = "card-payments"
	// ComposeInferredContractDir holds the Compose-only contract inside the artifact.
	ComposeInferredContractDir = ComposeArtifactMount + "/inferred/" + ComposeInferredConsumer
	composeInferredKeyID       = "compose-inferred"
	// composeInferredDir holds the installation key and the first-publish marker
	// in the state volume.
	composeInferredDir       = composeStateMount + "/inferred"
	composeInferredKey       = composeInferredDir + "/" + composeInferredKeyID + ".key"
	composeInferredPublished = composeInferredDir + "/published"
	// composeInferredInterval is the operator's InferredInterval.
	composeInferredInterval = 5 * time.Minute
)

// composeInferredContract is the Compose-only consumer: it declares the
// ref-less external stripe and pins stripe's inferred contract, so its
// stripe-pinned edge reads rangeStatus unknown with rangeReason
// INFERRED_PROVIDER end to end.
const composeInferredContract = `pactoVersion: '2.1'
service:
  name: ` + ComposeInferredConsumer + `
  version: 1.0.0
  owner:
    team: payments-team
dependencies:
- name: stripe
  external: true
  version: stripe-api/2023-10-16
  required: true
- name: stripe-pinned
  ref: oci://` + ComposeInferredRepository + `
  compatibility: ^0.0.1
  required: false
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`

// composeInferredInput is the Compose-only contract as a config entry.
var composeInferredInput = composeInput{name: "inferred_pacto.yaml", target: ComposeInferredContractDir + "/pacto.yaml", content: literal(composeInferredContract)}

// composeInferred is the service that mints the installation key on first
// start and publishes the inferred contract of every external system the
// seeded bundles and the Compose-only contract name, every
// composeInferredInterval. A failed publish is retried in seconds; the
// service is healthy once one publish succeeded.
func composeInferred(image string, seeded []string) composeService {
	publish := "pacto fleet publish-inferred --registry " + ComposeDomain + " --key " + composeInferredKey + " --local " + ComposeInferredContractDir
	for _, ref := range seeded {
		publish += " --oci " + ref
	}
	script := strings.Join([]string{
		"mkdir -p " + composeInferredDir,
		"[ -f " + composeInferredKey + " ] || pacto evidence keygen --out " + composeInferredDir + " --key-id " + composeInferredKeyID,
		"while :; do",
		"  if " + publish + "; then touch " + composeInferredPublished + "; sleep " + strconv.Itoa(int(composeInferredInterval/time.Second)) + "; else sleep 3; fi",
		"done",
	}, "\n") + "\n"
	return composeService{
		Image:       image,
		Restart:     "unless-stopped",
		Entrypoint:  []string{"/bin/sh", "-euc"},
		Command:     []string{script},
		Environment: map[string]string{"PACTO_INSECURE_REGISTRIES": ComposeRegistryHost},
		// The seeded bundles must be in the registry before the first read.
		DependsOn: map[string]composeDep{"seed": {Condition: "service_completed_successfully"}},
		Volumes:   []string{composeStateVolume + ":" + composeStateMount},
		Configs:   mounts([]composeInput{composeInferredInput}),
		Healthcheck: &composeHealth{
			Test:        []string{"CMD-SHELL", "test -f " + composeInferredPublished},
			Interval:    "3s",
			Timeout:     "3s",
			Retries:     40,
			StartPeriod: "60s",
		},
	}
}
