package pactify

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// delivery plans the Pacto resource that ships inside every version. The
// promoter creates each environment's Stage, Flux objects and deployer from
// the installation ladder, so init writes none of them. A chart deploy gets
// no Pacto resource here: the cluster's applier deploys it.
func (p *planner) delivery() {
	if p.deployRepo() == "" {
		p.noManifests()
		return
	}
	name, kind, target := p.r.Service, "Deployment", p.r.Service
	if w := p.sc.Workload; w != nil {
		kind, target = w.Kind, w.Name
	}
	// ponytail: only a Service named like the workload counts as its Service;
	// read Service selectors if services name theirs differently.
	service := ""
	if p.sc.Services[target] {
		service = "    serviceName: " + target + "\n"
	}
	p.deliveryPlanned = true
	pull := ""
	if s := p.opts.RegistrySecret; s != "" {
		pull = "    pullSecretRef: " + s + "\n"
		p.next("in each namespace delivery/pacto.yaml is applied to, create the Secret %s that logs in to the registry: kubectl -n <namespace> create secret docker-registry %s "+
			"--docker-server=<registry> --docker-username=<user> --docker-password=<token>; name it under imagePullSecrets in your Deployment too if the images are private", s, s)
		p.next("install delivery with promoter.registryCredentials.secretName set to a Secret like %s in its own namespace, so it can read the contracts", s)
	}
	p.file("delivery/pacto.yaml", fmt.Sprintf(pactoResource, name, name, p.pub.Bundle, pull, service, target, kind))
	if p.opts.InsecureRegistry {
		host, _, _ := strings.Cut(strings.TrimPrefix(p.pub.Bundle, "oci://"), "/")
		p.next("the registry %s speaks plain HTTP: list it in the chart value insecureRegistries, so the Pacto resource reads its contract over plain HTTP (a local registry only)", host)
	}
	p.next("Pacto creates each environment's Stage once delivery is on: see Delivery reference: environments")
	// A derived section ships delivery/pacto.yaml itself; a declared one ships
	// it only when one of its builds reads it.
	if !p.pubDerived && !p.shipsPactoResource() {
		p.next(`ship delivery/pacto.yaml with every version: add to the manifests build: sed "s/@PACTO_VERSION@/$PACTO_VERSION/" delivery/pacto.yaml > "$PACTO_OUT"/pacto-resource.yaml`)
	}
	for _, k := range p.sc.Kustomize {
		p.next("%s decides what Flux applies: add pacto-resource.yaml to its resources", k)
	}
}

// deployedTag is the version the workload runs, read from its image tag, or
// "" when the tag is not a version.
func (p *planner) deployedTag() string {
	if p.sc.Workload == nil {
		return ""
	}
	_, tag := discover.ImageRepo(p.sc.Workload.Image)
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

// noManifests says why a contract that deploys no manifests gets no
// cluster pieces: a chart is deployed by the cluster's own applier.
func (p *planner) noManifests() {
	if p.pub != nil && slices.ContainsFunc(p.pub.Artifacts, func(a contract.PublishArtifact) bool {
		return a.Name == p.pub.Deploy && a.Type == contract.ArtifactChart
	}) {
		p.next("publish.deploy names the chart %s, so Pacto wrote no applier object: deploy it with the applier your cluster runs and name that object in each Stage's spec.applyRef", p.pub.Deploy)
		return
	}
	p.unknown("the cluster pieces: the contract publishes no manifests (publish.deploy), so none were planned")
}

const pactoResource = `# The Pacto resource: it checks the running %s against the contract of the
# version that ships. It ships inside every version: pacto ci --publish
# fills in @PACTO_VERSION@. Its Service,
# when there is one, lets Pacto see the calls made to it.
apiVersion: pacto.trianalab.io/v1
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
