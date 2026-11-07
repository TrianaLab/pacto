package fleet

import (
	"iter"
	"strconv"

	"github.com/trianalab/pacto/v4/pkg/lock"
)

// declaresTemplate reports whether r's contract was read and declares
// service.template.
func declaresTemplate(r *ContractRevision) bool {
	return r != nil && r.Contract != nil && r.Contract.Service.Template != nil
}

// serviceRevisions is every revision of s.
func serviceRevisions(snap *FleetSnapshot, s *ServiceRecord) []*ContractRevision {
	out := make([]*ContractRevision, 0, len(s.Revisions))
	for _, rk := range s.Revisions {
		out = append(out, snap.Revisions[rk])
	}
	return out
}

// deriveTemplate marks s a golden path when its newest readable revision
// declares service.template and the fleet knows s runs nowhere: it has no
// target and placed says no Kubernetes source is unavailable or partial. While
// one is, a target-less service stays a service, so an outage never drops a
// running service from impact. Each revision of a golden path that declares
// the template gets its content hash, the digest a service.origin records.
func deriveTemplate(snap *FleetSnapshot, s *ServiceRecord, placed bool) {
	s.Template = placed && len(s.Targets) == 0 && declaresTemplate(newestReadable(serviceRevisions(snap, s)))
	if !s.Template {
		return
	}
	for _, rk := range s.Revisions {
		if r := snap.Revisions[rk]; declaresTemplate(r) && r.BundleFS() != nil {
			r.contentHash, _ = lock.HashFS(r.BundleFS())
		}
	}
}

// resolveOrigins sets each service's Origin from the service.origin of its
// newest readable revision, resolved to the golden path revision whose digest
// or content hash equals the declared digest. It runs after every service is
// classified.
func resolveOrigins(snap *FleetSnapshot) {
	byDigest := map[string][]*ContractRevision{}
	for _, r := range snap.Revisions {
		if !snap.Services[r.ServiceKey].Template || !declaresTemplate(r) {
			continue
		}
		for _, d := range []string{r.Digest, r.contentHash} {
			if d != "" {
				byDigest[d] = append(byDigest[d], r)
			}
		}
	}
	for _, s := range snap.Services {
		newest := newestReadable(serviceRevisions(snap, s))
		if newest == nil || newest.Contract.Service.Origin == nil {
			continue
		}
		o := newest.Contract.Service.Origin
		s.Origin = resolveOrigin(byDigest[o.Digest], o.Ref, o.Digest)
	}
}

// resolveOrigin resolves a declared origin among the golden path revisions
// that match its digest: exactly one golden path resolves it, to its lowest
// revision key when several of its revisions match.
func resolveOrigin(matches []*ContractRevision, ref, digest string) *ServiceOrigin {
	out := &ServiceOrigin{Ref: ref, Digest: digest}
	var match *ContractRevision
	paths := map[ServiceKey]bool{}
	for _, r := range matches {
		paths[r.ServiceKey] = true
		if match == nil || r.Key < match.Key {
			match = r
		}
	}
	switch len(paths) {
	case 0:
		out.Reason = "no golden path revision in this snapshot has digest " + digest
	case 1:
		out.GoldenPath, out.Revision = match.ServiceKey, match.Key
	default:
		out.Reason = "the digest matches more than one golden path"
	}
	return out
}

// declaredEdges is every declared and observed edge a revision's own
// references can resolve through: Relationships, then the golden path edges.
func (s *FleetSnapshot) declaredEdges() iter.Seq[*Relationship] {
	return func(yield func(*Relationship) bool) {
		for _, rels := range [][]Relationship{s.Relationships, s.templateEdges} {
			for i := range rels {
				if !yield(&rels[i]) {
					return
				}
			}
		}
	}
}

// goldenPath reports whether the service k is a golden path.
func (q *Query) goldenPath(k ServiceKey) bool {
	s := q.snap.Services[k]
	return s != nil && s.Template
}

// populationService reports whether k is a service the population summaries
// count: one in the snapshot that is no golden path.
func (q *Query) populationService(k ServiceKey) bool {
	s := q.snap.Services[k]
	return s != nil && !s.Template
}

// deployedTemplateAttention reports each service whose newest readable
// revision declares service.template while it runs: service.template is
// self-declared, so the service stays a service and a careless marker never
// silences impact.
func (q *Query) deployedTemplateAttention() []AttentionItem {
	var out []AttentionItem
	for _, s := range q.snap.Services {
		if len(s.Targets) == 0 || !declaresTemplate(newestReadable(serviceRevisions(q.snap, s))) {
			continue
		}
		targets := strconv.Itoa(len(s.Targets)) + " operational targets"
		if len(s.Targets) == 1 {
			targets = "1 operational target"
		}
		out = append(out, AttentionItem{
			Entity: serviceEntityRef(s), Service: string(s.Key), Label: s.Name,
			Severity: severityWarning, Code: "GOLDEN_PATH_DEPLOYED", Category: categoryGoldenPath,
			Summary:  "Running service declares service.template",
			Reason:   s.Name + " declares service.template but runs in " + targets + "; it is treated as a service",
			NextStep: "Remove service.template from the contract of " + s.Name + ", or stop running the golden path",
		})
	}
	return out
}
