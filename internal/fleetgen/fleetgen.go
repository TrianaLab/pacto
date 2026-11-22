// Package fleetgen builds large, deterministic fleets in memory, for the
// budget tests, the browser scale suite and the demo's large mode. Nothing it
// builds is published: every contract lives in an in-memory bundle.
package fleetgen

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// domainWords name the business domains, in order; a fleet uses the first
// clamp(n/170, 3, 60) of them.
var domainWords = [...]string{
	"platform", "payments", "checkout", "catalog", "search", "identity", "orders", "shipping", "billing", "pricing",
	"inventory", "fraud", "loyalty", "reviews", "media", "messaging", "analytics", "reporting", "support", "partners",
	"accounts", "ledger", "tax", "returns", "promotions", "recommend", "content", "mobile", "web", "gateway",
	"compliance", "risk", "treasury", "lending", "cards", "wallet", "rewards", "travel", "booking", "maps",
	"logistics", "fleet", "warehouse", "procure", "suppliers", "hr", "payroll", "legal", "security", "audit",
	"observe", "storage", "streaming", "events", "notify", "email", "sms", "voice", "video", "ml",
}

// envs are the persistent environments in promotion order; prod-us runs in
// the second cluster.
var envs = [...]string{"dev", "staging", "prod-eu", "prod-us"}

const (
	externals = 16
	registry  = "oci://registry.example/fleet/"
	clusterEU = "0a5e1c00-0000-4000-8000-00000000e001"
	clusterUS = "0a5e1c00-0000-4000-8000-00000000e002"
)

var openapiDoc = []byte("openapi: 3.0.3\ninfo:\n  title: api\n  version: 1.0.0\npaths:\n  /status:\n    get:\n      responses:\n        \"200\":\n          description: ok\n")

// gen holds one fleet while it is generated.
type gen struct {
	r        *rand.Rand
	now      time.Time
	declared int
	domains  int
	teams    int
	hubs     int
	// placed[i] lists the environments service i runs in, in order.
	placed [][]string
	// preview[i] is the preview environment service i declares, "" for none.
	preview []string
	col     *fleet.Collection
}

// Collection returns a fleet of n services (n at least 40; about 12% of
// them inferred workloads no contract describes) built from seed, with every
// time an offset from now. Read it as one [fleet.SourceKindKubernetes] source: it carries the
// revisions, the targets of two clusters, the inferred workloads and the
// observed calls a node kernel would report. The same n and seed give the
// same collection.
//
// ponytail: fixed proportions; #600 adds profiles.
func Collection(n int, seed uint64, now time.Time) *fleet.Collection {
	inferred := n * 12 / 100
	g := &gen{
		r: rand.New(rand.NewPCG(seed, uint64(n))), now: now, declared: n - inferred,
		domains: min(max(n/170, 3), len(domainWords)), teams: max(n/25, 1),
		col: &fleet.Collection{Cluster: &fleet.Cluster{ID: clusterEU, Name: "eu", NameSource: fleet.ClusterNameFromContext}},
	}
	g.hubs = max(g.declared/50, 2)
	g.place(n / 100)
	deps := make([][]int, g.declared)
	for i := range g.declared {
		deps[i] = g.dependencies(i)
		g.revision(i, deps[i])
		g.targets(i)
	}
	g.observe(deps)
	g.inferred(inferred)
	return g.col
}

// name is the service name of declared service i.
func (g *gen) name(i int) string { return fmt.Sprintf("%s-%05d", g.domain(i), i) }

// domain is the business domain of declared service i: its team's.
func (g *gen) domain(i int) string {
	if i < g.hubs {
		return domainWords[0]
	}
	return domainWords[(i%g.teams)%g.domains]
}

// team is the owner team of declared service i.
func (g *gen) team(i int) string {
	if i < g.hubs {
		return "platform-core"
	}
	return fmt.Sprintf("%s-team-%03d", g.domain(i), i%g.teams)
}

// place picks the environments each service runs in (1 to 4) and gives
// previews services a preview environment.
func (g *gen) place(previews int) {
	g.placed = make([][]string, g.declared)
	g.preview = make([]string, g.declared)
	for i := range g.declared {
		k := 1 + g.r.IntN(len(envs))
		g.placed[i] = slices.Clone(envs[len(envs)-k:])
		if i < g.hubs {
			g.placed[i] = slices.Clone(envs[:])
		}
	}
	for p := range previews {
		// 37 is prime and previews < (declared-hubs)/37, so no two collide.
		g.preview[g.hubs+(p*37)%(g.declared-g.hubs)] = fmt.Sprintf("pr-%d", p+1)
	}
}

// dependencies picks about 2.5 providers for service i: one platform hub,
// then up to three others.
func (g *gen) dependencies(i int) []int {
	out := []int{(i + 1) % g.hubs}
	for range g.r.IntN(4) {
		if j := g.r.IntN(g.declared); j != i && !slices.Contains(out, j) {
			out = append(out, j)
		}
	}
	if i == g.declared-1 && !slices.Contains(out, 0) {
		out = append(out, 0) // the consumer the one breaks block names
	}
	return out
}

// digest is the content digest of declared service i's revision.
func (g *gen) digest(i int) string { return fmt.Sprintf("sha256:%064x", i+1) }

// revision adds the contract of declared service i.
func (g *gen) revision(i int, deps []int) {
	yml, files := g.contractYAML(i, deps)
	files["pacto.yaml"] = &fstest.MapFile{Data: yml}
	// ponytail: the YAML is ours and TestFleetgenValidContracts parses and
	// validates every contract, so a parse error cannot reach here.
	c, doc, _ := contract.ParseDocument(yml)
	g.col.Revisions = append(g.col.Revisions, fleet.RawRevision{
		Bundle: &contract.Bundle{Contract: c, RawYAML: yml, FS: files}, Document: doc,
		RequestedRef: registry + g.name(i) + ":" + c.Service.Version,
		ResolvedRef:  registry + g.name(i) + "@" + g.digest(i), Digest: g.digest(i),
		FetchedAt: new(g.now.Add(-time.Hour)),
	})
}

// contractYAML is the pacto.yaml of declared service i and its other files.
func (g *gen) contractYAML(i int, deps []int) ([]byte, fstest.MapFS) {
	files := fstest.MapFS{"interfaces/openapi.yaml": {Data: openapiDoc}}
	var b strings.Builder
	fmt.Fprintf(&b, "pactoVersion: \"2.5\"\nservice:\n  name: %s\n  version: 1.%d.0\n  domain: %s\n  summary: Serves %s requests\n  owner:\n    team: %s\n",
		g.name(i), i%7, g.domain(i), g.domain(i), g.team(i))
	b.WriteString("interfaces:\n  - name: api\n    type: openapi\n    ref: interfaces/openapi.yaml\n")
	b.WriteString("environments:\n")
	for _, e := range g.placed[i] {
		fmt.Fprintf(&b, "  - name: %s\n    file: environments/%s.yaml\n", e, e)
		files["environments/"+e+".yaml"] = &fstest.MapFile{Data: []byte("{}\n")}
	}
	if p := g.preview[i]; p != "" {
		fmt.Fprintf(&b, "  - name: %s\n    file: environments/%s.yaml\n    lifetime: temporary\n", p, p)
		files["environments/"+p+".yaml"] = &fstest.MapFile{Data: []byte("{}\n")}
	}
	g.dependenciesYAML(&b, i, deps)
	if i == 0 && g.declared > 1 {
		fmt.Fprintf(&b, "breaks:\n  - consumers: [%s]\n    reason: removes PUT /cache; read through GET /cache/{key}\n", g.name(g.declared-1))
	}
	b.WriteString("workload: service\ncapabilities:\n  - type: health\n")
	if i%4 != 3 {
		fmt.Fprintf(&b, "readiness:\n  minScore: 80\n  expires: \"2099-12-31\"\n  claims:\n    - id: dashboard\n      type: url\n      category: observability\n      status: done\n      evidence: https://grafana.example/d/%s\n      weight: 100\n", g.name(i))
	}
	return []byte(b.String()), files
}

// dependenciesYAML writes the dependencies block: every 97th service accepts
// a major its provider does not run, and every 40th calls an external system.
func (g *gen) dependenciesYAML(b *strings.Builder, i int, deps []int) {
	b.WriteString("dependencies:\n")
	for k, j := range deps {
		compat := "^1.0.0"
		if k == 0 && i%97 == 5 {
			compat = "^2.0.0"
		}
		fmt.Fprintf(b, "  - name: %s\n    ref: %s%s\n    required: true\n    compatibility: \"%s\"\n", g.name(j), registry, g.name(j), compat)
	}
	if i%40 == 7 {
		e := (i / 40) % externals
		fmt.Fprintf(b, "  - name: ext-%02d\n    external: true\n    identity: host/ext-%02d.example.com\n    version: \"2025-01\"\n    required: false\n", e, e)
	}
}

// targetName is the target (and workload) name of service i in env.
func (g *gen) targetName(i int, env string) string { return g.name(i) + "-" + env }

// workload is the observed name of service i's workload in env.
func (g *gen) workload(i int, env string) string {
	cl := clusterEU
	if env == "prod-us" {
		cl = clusterUS
	}
	return fleet.K8sCaller(cl, g.domain(i), "Deployment", g.targetName(i, env))
}

// targets adds the targets of declared service i: every 23rd non-compliant
// and every 31st failing its health check.
func (g *gen) targets(i int) {
	us := &fleet.Cluster{ID: clusterUS, Name: "us", NameSource: fleet.ClusterNameFromContext}
	envsOf := g.placed[i]
	if g.preview[i] != "" {
		envsOf = append(slices.Clone(envsOf), g.preview[i])
	}
	for _, env := range envsOf {
		k := len(g.col.Targets)
		t := fleet.RawTarget{
			Scope: g.domain(i), Kind: fleet.SourceKindKubernetes, Name: g.targetName(i, env),
			Labels:  map[string]string{fleet.TargetEnvironmentLabel: env},
			Service: g.name(i), ResolvedRef: registry + g.name(i) + "@" + g.digest(i), Digest: g.digest(i),
			Compliance: fleet.StatusCompliant, Coverage: &fleet.Coverage{Evaluated: 1, Required: 1},
			EvidenceAt: new(g.now.Add(-time.Minute)), Workload: &fleet.WorkloadRef{Kind: "Deployment", Name: g.targetName(i, env)},
		}
		if env == "prod-us" {
			t.Cluster = us
		}
		if k%23 == 11 {
			t.Compliance = fleet.StatusNonCompliant
		}
		if k%31 == 13 {
			t.Findings = []finding.Finding{{Code: finding.CodeHealthFailing, Severity: finding.SeverityUnknown, Category: finding.CategoryInconclusive,
				Subject: finding.SubjectRef{Kind: "capability", Name: contract.CapabilityHealth}, Message: "readiness probe failing"}}
		}
		g.col.Targets = append(g.col.Targets, t)
	}
}

// call is one observed call last seen a minute ago.
func (g *gen) call(from, to string) fleet.ObservedEdge {
	return fleet.ObservedEdge{From: from, To: to, Count: 1 + g.r.IntN(500), FirstSeen: g.now.Add(-time.Hour), LastSeen: g.now.Add(-time.Minute)}
}

// observe adds the observed calls: about 70% of the declared edges, in an
// environment both ends run in; 5% more calls no contract declares; and a few
// calls across environments, a third of them into a preview.
func (g *gen) observe(deps [][]int) {
	for i, ds := range deps {
		for _, j := range ds {
			if g.r.IntN(10) < 7 {
				env := g.shared(i, j)
				g.col.Observed = append(g.col.Observed, g.call(g.workload(i, env), g.workload(j, env)))
			}
		}
	}
	for range g.declared / 20 {
		i, j := g.r.IntN(g.declared), g.r.IntN(g.declared)
		if i != j && !slices.Contains(deps[i], j) {
			env := g.shared(i, j)
			g.col.Observed = append(g.col.Observed, g.call(g.workload(i, env), g.workload(j, env)))
		}
	}
	previews := 0
	for i := range g.declared {
		if g.preview[i] == "" || previews == max(g.declared/1500, 1) {
			continue
		}
		previews++
		g.col.Observed = append(g.col.Observed, g.call(g.workload(0, "staging"), g.workload(i, g.preview[i])))
	}
	for k := range max(g.declared/500, 2) {
		i := g.hubs + (k*53)%(g.declared-g.hubs)
		g.col.Observed = append(g.col.Observed, g.call(g.workload(i, g.placed[i][len(g.placed[i])-1]), g.workload(k%g.hubs, "dev")))
	}
}

// shared is an environment services i and j both run in: placements are
// suffixes of envs, so the shorter one is inside the longer.
func (g *gen) shared(i, j int) string {
	a := g.placed[i]
	if len(g.placed[j]) < len(a) {
		a = g.placed[j]
	}
	return a[g.r.IntN(len(a))]
}

// inferred adds m workloads no contract describes, in the first cluster, each
// calling one declared service.
func (g *gen) inferred(m int) {
	for k := range m {
		ns := domainWords[k%g.domains]
		name := fmt.Sprintf("legacy-%05d", k)
		g.col.InferredServices = append(g.col.InferredServices, fleet.RawInferredService{
			Namespace: ns, Kind: "Deployment", Name: name,
			Image:      &fleet.InferredValue{Value: "registry.example/legacy/" + name + ":1.0.0", From: "spec.template.spec.containers[0].image"},
			ObservedAt: new(g.now.Add(-time.Minute)),
		})
		j := g.r.IntN(g.declared)
		g.col.Observed = append(g.col.Observed, g.call(fleet.K8sCaller(clusterEU, ns, "Deployment", name), g.workload(j, g.placed[j][0])))
	}
}
