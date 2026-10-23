package pactify

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// delivery plans the cluster pieces: the Pacto resource that ships inside
// every version, and per environment the Flux OCIRepository new versions come
// from, the one Pacto points at the version to run, the Kustomization that
// applies it and the Stage that decides.
func (p *planner) delivery() {
	deployRepo := ""
	if p.pub != nil {
		for _, a := range p.pub.Artifacts {
			if a.Name == p.pub.Deploy && a.Type == contract.ArtifactManifests {
				deployRepo = a.Ref
			}
		}
	}
	if deployRepo == "" {
		p.unknown("the cluster pieces: the contract publishes no manifests for Flux to apply (publish.deploy), so none were planned")
		return
	}
	name, kind, target := p.r.Service, "Deployment", p.r.Service
	if w := p.sc.workload; w != nil {
		kind, target = w.kind, w.name
	}
	// ponytail: only a Service named like the workload counts as its Service;
	// read Service selectors if services name theirs differently.
	service := ""
	if p.sc.services[target] {
		service = "    serviceName: " + target + "\n"
	}
	p.deliveryPlanned = true
	p.file("delivery/pacto.yaml", fmt.Sprintf(pactoResource, name, name, p.pub.Bundle, service, target, kind))
	verify := p.verify()
	for i, env := range p.envs {
		var b strings.Builder
		fmt.Fprintf(&b, "# %s in %s: what applies it and the Stage that decides which version runs.\n# Written by pacto init.\n", name, env)
		candidates := fmt.Sprintf("    upstreamStage:\n      name: %s\n      namespace: %s\n", name, p.envs[max(i-1, 0)])
		if i == 0 {
			fmt.Fprintf(&b, candidateRepo, name, env, deployRepo, verify)
			candidates = fmt.Sprintf("    ociRepositoryRef:\n      name: %s-candidates\n", name)
		}
		fmt.Fprintf(&b, envPieces, name, env, name, deployRepo, verify, name, env, name, env, name, env, name, env, name, candidates)
		p.file("delivery/"+env+".yaml", b.String())
		want := "the registry"
		if i > 0 {
			want = "the Stage in " + p.envs[i-1]
		}
		p.keptSource(env, want)
	}
	envs := strings.Join(p.envs, ", ")
	p.next("create the namespaces %s, then apply each environment: kubectl apply -f delivery/%s.yaml", envs, strings.Join(p.envs, ".yaml -f delivery/"))
	if !p.contractNew && !p.shipsPactoResource() {
		p.next(`ship delivery/pacto.yaml with every version: add to the manifests build: sed "s/@PACTO_VERSION@/$PACTO_VERSION/" delivery/pacto.yaml > "$PACTO_OUT"/pacto-resource.yaml`)
	}
	for _, k := range p.sc.kustomize {
		p.next("%s decides what Flux applies: add pacto-resource.yaml to its resources", k)
	}
}

// keptSource warns when a delivery/<env>.yaml already there takes its
// candidates from somewhere other than want, the place --env now gives it:
// the first environment reads the registry, every other the one before it.
func (p *planner) keptSource(env, want string) {
	data, err := os.ReadFile(filepath.Join(p.r.Dir, "delivery", env+".yaml"))
	if err != nil {
		return // planned as new
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc map[string]any
		if dec.Decode(&doc) != nil {
			return
		}
		if doc["kind"] != "Stage" {
			continue
		}
		got := "the registry"
		if up, ok := dig(doc, "spec", "candidates", "upstreamStage").(map[string]any); ok {
			ns, _ := up["namespace"].(string)
			got = "the Stage in " + ns
		}
		if got != want {
			p.next("delivery/%s.yaml was left as it is, but its Stage takes candidates from %s, not %s as --env %s orders them; "+
				"change its spec.candidates, or move the file away and run pacto init --delivery again to rewrite it", env, got, want, strings.Join(p.envs, ","))
		}
		return
	}
}

// verify is the Flux spec.verify that names the publisher, so a Stage
// promotes only what pacto publish signed.
func (p *planner) verify() string {
	s := p.pub.Sign
	if s != nil && s.Keyless && s.Identity != "" && s.Issuer != "" {
		return fmt.Sprintf("  verify:\n    provider: cosign\n    matchOIDCIdentity:\n      - issuer: %s\n        subject: %s\n",
			yq("^"+regexp.QuoteMeta(s.Issuer)+"$"), yq("^"+regexp.QuoteMeta(s.Identity)+"$"))
	}
	if s == nil || s.Keyless {
		p.unknown("who publishes %s: the contract names no signing key or keyless identity, so the Stages cannot tell its versions from anyone else's and promote nothing until publish.sign says", p.r.Service)
	} else {
		p.next("in each of %s, create the Secret cosign-pub holding the public key: kubectl -n <env> create secret generic cosign-pub --from-file=cosign.pub", strings.Join(p.envs, ", "))
	}
	return "  verify:\n    provider: cosign\n    secretRef:\n      name: cosign-pub\n"
}

const pactoResource = `# The Pacto resource: it checks the running %s against the contract of the
# version that ships. It ships inside every version: the manifests build in
# pacto.yaml fills in @PACTO_VERSION@ when pacto publish runs it. Its Service,
# when there is one, lets Pacto see the calls made to it.
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata:
  name: %s
spec:
  contractRef:
    oci: %s:@PACTO_VERSION@
  target:
%s    workloadRef:
      name: %s
      kind: %s
`

const candidateRepo = `# New versions: every version pacto publish signed.
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: %s-candidates
  namespace: %s
spec:
  interval: 1m
  url: %s
  ref:
    semver: ">=0.0.0"
%s---
`

const envPieces = `# The version that runs. Only Pacto sets spec.ref.digest; until the first
# promote there is none, so Flux applies nothing.
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: %s
  namespace: %s
  labels:
    delivery.pacto.trianalab.io/apply-ref: %s
spec:
  interval: 1m
  url: %s
%s---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: %s
  namespace: %s
spec:
  interval: 10m
  sourceRef:
    kind: OCIRepository
    name: %s
  path: ./
  prune: true
  targetNamespace: %s
  # Ready only once what it applied is rolled out, so the Stage judges the new pods.
  wait: true
  timeout: 2m
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata:
  name: %s
  namespace: %s
spec:
  service:
    name: %s
  environment: %s
  applyRef:
    name: %s
  candidates:
%s  auto: true
  gates:
    verify:
      window: 5m
`

// shipsPactoResource says whether a build in the contract already reads
// delivery/pacto.yaml.
func (p *planner) shipsPactoResource() bool {
	for _, a := range p.pub.Artifacts {
		if strings.Contains(strings.Join(a.Build.Command, " "), "delivery/pacto.yaml") {
			return true
		}
	}
	return false
}
