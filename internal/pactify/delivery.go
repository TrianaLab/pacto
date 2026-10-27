package pactify

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// delivery plans the cluster pieces: the Pacto resource that ships inside
// every version, and per environment the Flux OCIRepository new versions come
// from, the one Pacto points at the version to run, the Kustomization that
// applies it and the Stage that decides.
func (p *planner) delivery() {
	deployRepo := p.deployRepo()
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
	pull := ""
	verify := p.verify()
	if s := p.opts.RegistrySecret; s != "" {
		pull = "    pullSecretRef: " + s + "\n"
		verify = "  # The registry login, for a private registry.\n  secretRef:\n    name: " + s + "\n" + verify
		p.next("in each of %s, create the Secret %s that logs in to the registry: kubectl -n <env> create secret docker-registry %s "+
			"--docker-server=<registry> --docker-username=<user> --docker-password=<token>; name it under imagePullSecrets in your Deployment too if the images are private", strings.Join(p.envs, ", "), s, s)
		p.next("install delivery with promoter.registryCredentials.secretName set to a Secret like %s in its own namespace, so it can read the contracts", s)
	}
	p.file("delivery/pacto.yaml", fmt.Sprintf(pactoResource, name, name, p.pub.Bundle, pull, service, target, kind))
	if p.opts.InsecureRegistry {
		verify = "  # The registry speaks plain HTTP: for a local registry only.\n  insecure: true\n" + verify
	}
	tag := p.deployedTag()
	if tag == "" {
		// An OCIRepository with no ref follows latest, so the Stage would
		// watch latest and not what the team ships.
		p.next("delivery/%s.yaml was not written: no workload image tag is a version (x.y.z), so nothing says which version you deploy, "+
			"and an OCIRepository with no ref follows latest. Tag the image with the version you run and run pacto init --delivery again", strings.Join(p.envs, ".yaml, delivery/"))
		return
	}
	for i, env := range p.envs {
		var b strings.Builder
		fmt.Fprintf(&b, "# %s in %s: what applies it and the Stage that decides which version runs.\n# Written by pacto init.\n", name, env)
		candidates := fmt.Sprintf("    upstreamStage:\n      name: %s\n      namespace: %s\n", name, p.envs[max(i-1, 0)])
		if i == 0 {
			fmt.Fprintf(&b, candidateRepo, name, env, deployRepo, verify)
			candidates = fmt.Sprintf("    ociRepositoryRef:\n      name: %s-candidates\n", name)
		}
		approval := ""
		if i == len(p.envs)-1 {
			approval = gated
		}
		fmt.Fprintf(&b, deployer, name, env)
		fmt.Fprintf(&b, envPieces, name, env, deployRepo, tag, verify, name, env, name, env, name, name, env, name, env, name, kind, target, candidates, approval)
		p.file("delivery/"+env+".yaml", b.String())
		want := "the registry"
		if i > 0 {
			want = "the Stage in " + p.envs[i-1]
		}
		p.keptSource(env, want)
	}
	envs := strings.Join(p.envs, ", ")
	last := p.envs[len(p.envs)-1]
	p.next("each Stage starts at observe: it plans every change and moves nothing, and GitOps keeps writing spec.ref of the %s OCIRepository. "+
		"To leave observe, remove spec.ref from that OCIRepository in git (on Argo CD, ignoreDifferences on targetRevision) and label it %s=%s, "+
		"so admission protects the pointer before Pacto owns it. Once the label is on and 10 versions GitOps applied verified (autonomy.proposeAfter), "+
		"Pacto proposes advise: raise it with pacto delivery propose --type delivery.pacto.trianalab.io/autonomy --stage <stage> --to advise, "+
		"and have a second person approve it", name, applyRefLabel, name)
	p.next("every promote to %s waits for a person to approve it (approval.always in delivery/%s.yaml); "+
		"once a health check judges each version there (gates.verify.checks, see Delivery reference: health checks), delete approval, so it promotes on its own at a level above advise", last, last)
	p.next("create the namespaces %s, then apply each environment: kubectl apply -f delivery/%s.yaml", envs, strings.Join(p.envs, ".yaml -f delivery/"))
	if !p.contractNew && !p.shipsPactoResource() {
		p.next(`ship delivery/pacto.yaml with every version: add to the manifests build: sed "s/@PACTO_VERSION@/$PACTO_VERSION/" delivery/pacto.yaml > "$PACTO_OUT"/pacto-resource.yaml`)
	}
	for _, k := range p.sc.kustomize {
		p.next("%s decides what Flux applies: add pacto-resource.yaml to its resources", k)
	}
}

// applyRefLabel marks the OCIRepository Pacto may write a digest to.
const applyRefLabel = "delivery.pacto.trianalab.io/apply-ref"

// deployedTag is the version the workload runs, read from its image tag, or
// "" when the tag is not a version.
func (p *planner) deployedTag() string {
	if p.sc.workload == nil {
		return ""
	}
	_, tag := imageRepo(p.sc.workload.image)
	v, err := semver.StrictNewVersion(strings.TrimPrefix(tag, "v"))
	if err != nil {
		return ""
	}
	return v.String()
}

// deployRepo is the repository of the manifests artifact publish.deploy
// names, or "" when the contract publishes none.
func (p *planner) deployRepo() string {
	if p.pub == nil {
		return ""
	}
	repo := ""
	for _, a := range p.pub.Artifacts {
		if a.Name == p.pub.Deploy && a.Type == contract.ArtifactManifests {
			repo = a.Ref
		}
	}
	return repo
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
%s  target:
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

// deployer is the account Flux applies a version as, and what it may touch:
// the workload kinds in its own namespace, nothing cluster-wide and nothing
// of Flux or Pacto delivery. Without it Flux applies as kustomize-controller,
// cluster-admin in a default install, so anyone who can publish a version
// could ship a ClusterRoleBinding and have it applied with no person asked.
const deployer = `# The account Flux applies each version of %[1]s as, in %[2]s. Widen the Role in
# git if your manifests need more; never give it cluster-wide rights or
# the delivery, Flux or RBAC groups.
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %[1]s-deployer
  namespace: %[2]s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: %[1]s-deployer
  namespace: %[2]s
rules:
  - apiGroups: [""]
    resources: [configmaps, secrets, services, serviceaccounts, persistentvolumeclaims]
    verbs: ["*"]
  - apiGroups: [apps]
    resources: [deployments, statefulsets, daemonsets]
    verbs: ["*"]
  - apiGroups: [batch]
    resources: [jobs, cronjobs]
    verbs: ["*"]
  - apiGroups: [autoscaling]
    resources: [horizontalpodautoscalers]
    verbs: ["*"]
  - apiGroups: [policy]
    resources: [poddisruptionbudgets]
    verbs: ["*"]
  - apiGroups: [networking.k8s.io]
    resources: [ingresses, networkpolicies]
    verbs: ["*"]
  - apiGroups: [pacto.trianalab.io]
    resources: [pactos]
    verbs: ["*"]
  # Flux checks a rollout's health as this account, reading its ReplicaSets and Pods.
  - apiGroups: [apps]
    resources: [replicasets]
    verbs: [get, list, watch]
  - apiGroups: [""]
    resources: [pods]
    verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: %[1]s-deployer
  namespace: %[2]s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: %[1]s-deployer
subjects:
  - kind: ServiceAccount
    name: %[1]s-deployer
---
`

const envPieces = `# The version that runs. GitOps owns spec.ref: it is the tag you deploy today,
# so while the Stage observes, Flux applies what it always did. Pacto writes a
# digest here only once this OCIRepository carries the apply-ref label.
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: %s
  namespace: %s
spec:
  interval: 1m
  url: %s
  ref:
    tag: %s
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
  # Flux applies each version as this account, never as itself: a version
  # can then change only what the deployer Role above allows, in this
  # namespace. An auto level judges the contract, not the manifests.
  serviceAccountName: %s-deployer
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
  # The workload the checks judge: a Pacto that checked anything else, such as
  # a decoy a version shipped, counts for nothing.
  workload:
    kind: %s
    name: %s
  candidates:
%s  # The Stage plans every change and moves nothing. Every level above this is
  # a delivery.pacto.trianalab.io/autonomy operation a second person approves.
  startLevel: observe
  gates:
    verify:
      window: 5m
%s`

// gated is the end of the last environment's Stage: autonomy there is earned,
// not granted on day one, so every promote waits for a person until a health
// check judges the version.
const gated = `  # Every promote here waits for a person: the contract check alone cannot
  # see a consumer with no contract, or whether the version is healthy. Add a
  # health check under gates.verify.checks, then delete approval to let
  # versions that pass it move on their own.
  approval:
    always: true
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
