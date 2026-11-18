package discover

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// ClusterFacts is what one cluster read shows about the workload Object
// ("<ns>/<Kind>/<name>") beyond the folder pactify drafted from it: plain
// data, never a value read from an environment or an argument. Only
// pactify passes it; pacto ci completes from the folder alone.
type ClusterFacts struct {
	Object     string
	AsOf       time.Time
	CallWindow time.Duration
	MaxItems   int
	// Snapshots are the folder files, by slash path, written from the
	// cluster read: their fields are cluster facts, not repository inputs.
	Snapshots []string
	Images    []ClusterImage
	Calls     []ClusterCall
	Coverage  []contract.InferredCoverage
	Hidden    contract.InferredHidden
}

// ClusterImage is one image digest a container of the workload's pods
// runs; Partial when the pods were not all read.
type ClusterImage struct {
	Container, Digest string
	Partial           bool
}

// ClusterCall is a service the workload was seen calling: its dependency
// name and ref, the range proposed from the Versions the fleet reported
// (display text), when the observations are from and whether they are
// partial. It never names the callee's namespace or Service.
type ClusterCall struct {
	Name, Ref, Proposed, Versions string
	AsOf                          time.Time
	Partial                       bool
}

// clusterLabel is the label key the workload reader takes the team from.
const clusterLabel = "team"

// CompleteCluster is CompleteIn over the folder dir, as its own repository
// root, with the cluster facts added: each field read from a snapshot file
// becomes a cluster fact with its asOf, and the image digests, the calls
// and their proposed ranges are added for keys neither the folder record
// nor declared holds. It sets the cluster input, coverage and hidden
// counts, and is nil only when there is nothing to record and every source
// was read in full.
func CompleteCluster(dir string, declared *contract.Contract, in contract.InferredInputs, cat *externalcatalog.Catalog, facts ClusterFacts) (*contract.InferredRecord, error) {
	rec, err := CompleteIn(dir, dir, declared, in, cat)
	if err != nil {
		return nil, err
	}
	empty := rec == nil
	if empty {
		in.Discovery, in.Catalog = Version, CatalogInputs(cmp.Or(cat, externalcatalog.Default()))
		rec = &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: in}
	}
	asOf := clusterTime(facts.AsOf)
	stampSnapshots(rec, facts, asOf)
	n := len(rec.Fields)
	addImages(rec, facts, asOf)
	addCalls(rec, cmp.Or(declared, &contract.Contract{}), facts, asOf)
	if empty && len(rec.Fields) == n && !slices.ContainsFunc(facts.Coverage, func(c contract.InferredCoverage) bool { return c.State != contract.CoverageComplete }) {
		return nil, nil
	}
	rec.Inputs.Cluster = &contract.InferredClusterInput{AsOf: asOf, Object: facts.Object, CallWindow: shortDuration(facts.CallWindow), MaxItems: facts.MaxItems}
	rec.Coverage = slices.Clone(facts.Coverage)
	if facts.Hidden != (contract.InferredHidden{}) {
		hidden := facts.Hidden
		rec.Hidden = &hidden
	}
	slices.SortFunc(rec.Fields, func(a, b contract.Inferred) int {
		return cmp.Or(strings.Compare(a.Field, b.Field), strings.Compare(a.Source, b.Source))
	})
	return rec, nil
}

// stampSnapshots turns each field read from a snapshot file into a cluster
// fact of the same value: the team label as "cluster:label", the rest as
// "cluster:manifest", and takes the snapshots out of the inputs.
func stampSnapshots(rec *contract.InferredRecord, facts ClusterFacts, asOf string) {
	for i, f := range rec.Fields {
		file, _, ok := strings.Cut(f.Source, ":")
		if !ok || !slices.Contains(facts.Snapshots, file) {
			continue
		}
		f.Source, f.AsOf = "cluster:manifest "+facts.Object+" "+f.Source, asOf
		if f.Field == "service.owner" && f.Class == "" { // an owner file has a class; a manifest's owner is its team label
			f.Source = "cluster:label " + facts.Object + " " + clusterLabel
		}
		rec.Fields[i] = f
	}
	rec.Inputs.Sources = slices.DeleteFunc(slices.Clone(rec.Inputs.Sources), func(s contract.InferredSourceFile) bool {
		return slices.Contains(facts.Snapshots, s.Path)
	})
}

// addImages adds image.digest[<container>]: each container's distinct
// digests, sorted. A digest is an observed fact with no declaring snippet.
func addImages(rec *contract.InferredRecord, facts ClusterFacts, asOf string) {
	digests, partial := map[string][]string{}, map[string]bool{}
	for _, img := range facts.Images {
		if !slices.Contains(digests[img.Container], img.Digest) {
			digests[img.Container] = append(digests[img.Container], img.Digest)
		}
		partial[img.Container] = partial[img.Container] || img.Partial
	}
	for container, ds := range digests {
		slices.Sort(ds)
		addField(rec, contract.Inferred{Field: "image.digest[" + container + "]", Value: strings.Join(ds, ", "),
			Source: "cluster:runtime pods of " + facts.Object, Confidence: contract.ConfidenceLikely, AsOf: asOf,
			Completeness: completeness(partial[container])})
	}
}

// addCalls adds dependencies[<name>] for each call the contract does not
// declare, and dependencies[<name>].compatibility for each proposed range
// unless the contract declares a compatibility for that dependency.
func addCalls(rec *contract.InferredRecord, d *contract.Contract, facts ClusterFacts, asOf string) {
	for _, call := range facts.Calls {
		at := cmp.Or(clusterTime(call.AsOf), asOf)
		dep, declared := declaredDependencyOf(d, call.Name)
		if !declared {
			addField(rec, contract.Inferred{Field: "dependencies[" + call.Name + "]", Value: call.Ref,
				Source: "cluster:runtime calls from " + facts.Object, Confidence: contract.ConfidenceLikely, AsOf: at,
				Completeness: completeness(call.Partial),
				Declare:      "dependencies:\n" + DependencyYAML(call.Name, call.Ref, "seen calling "+call.Name+" at runtime")})
		}
		if call.Proposed == "" || dep.Compatibility != "" && dep.Compatibility != contract.CompatibilityUnknown {
			continue
		}
		addField(rec, contract.Inferred{Field: "dependencies[" + call.Name + "].compatibility", Value: "compatibility " + contract.CompatibilityUnknown,
			Proposed: call.Proposed, Source: "cluster:fleet reported versions " + call.Versions, Confidence: contract.ConfidenceLikely,
			AsOf: at, Completeness: completeness(call.Partial), Declare: "compatibility: " + strconv.Quote(call.Proposed)})
	}
}

// addField adds f unless the record already holds its key: a folder fact
// is never outranked or annotated by a cluster fact.
func addField(rec *contract.InferredRecord, f contract.Inferred) {
	if !slices.ContainsFunc(rec.Fields, func(g contract.Inferred) bool { return g.Field == f.Field }) {
		rec.Fields = append(rec.Fields, f)
	}
}

// declaredDependencyOf is the dependency d declares under name, by the
// rule declaredDependency applies.
func declaredDependencyOf(d *contract.Contract, name string) (contract.Dependency, bool) {
	i := slices.IndexFunc(d.Dependencies, func(dep contract.Dependency) bool {
		return declaredDependency(&contract.Contract{Dependencies: []contract.Dependency{dep}}, name)
	})
	if i < 0 {
		return contract.Dependency{}, false
	}
	return d.Dependencies[i], true
}

func completeness(partial bool) string {
	if partial {
		return contract.CoveragePartial
	}
	return contract.CoverageComplete
}

// clusterTime is t in RFC 3339 UTC to the second, or "" when t is zero.
func clusterTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

// shortDuration is d without its zero minutes and seconds: 24h, not 24h0m0s.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}
