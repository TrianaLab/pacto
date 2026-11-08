package fleet

import (
	"cmp"
	"iter"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
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
// target and placed says no source that reports targets is unavailable or
// partial. While one is, a target-less service stays a service, so an outage never drops a
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

// createdCounts is, per golden path, how many services of the snapshot
// declare an origin that resolves to it. A golden path derived from another
// is left out, like the Services list the count opens leaves it out.
func (q *Query) createdCounts() map[ServiceKey]int {
	out := map[ServiceKey]int{}
	for _, s := range q.snap.Services {
		if !s.Template && s.Origin != nil && s.Origin.GoldenPath != "" {
			out[s.Origin.GoldenPath]++
		}
	}
	return out
}

// createdFrom reports whether the service k declares an origin that resolves
// to the golden path path.
func (q *Query) createdFrom(k, path ServiceKey) bool {
	o := q.snap.Services[k].Origin
	return o != nil && o.GoldenPath == path
}

// pathSignature is the signature status of the newest readable revision of
// the golden path s, not-checked when no source that read it checks
// signatures.
func (q *Query) pathSignature(s *ServiceRecord) string {
	if r := newestReadable(serviceRevisions(q.snap, s)); r != nil && r.Provenance != nil {
		return r.Provenance.Signature.Status
	}
	return SignatureNotChecked
}

// selfDeclaredSignature is what a golden path's signature check proves.
const selfDeclaredSignature = "The signer is the one the golden path's own contract declares; Pacto did not check it against a trust config."

// templateDetail is the golden path detail of s, nil when s is no golden path.
// It reads the newest readable revision: what it names, what it requires and
// how to create a service from exactly its bytes.
func (q *Query) templateDetail(s *ServiceRecord) *TemplateDetail {
	rev := newestReadable(serviceRevisions(q.snap, s))
	if !s.Template || rev == nil {
		return nil
	}
	var edges []Relationship
	hidden := map[ServiceKey]bool{}
	for _, rel := range q.snap.templateEdges {
		if rel.FromRevision == rev.Key {
			edges = append(edges, rel)
		}
		if rel.FromRevision == rev.Key && rel.Hidden {
			hidden[rel.hiddenTo] = true
		}
	}
	reqs, _ := q.offeringUsesIn(edges, func(*ServiceRecord) bool { return true }, s.Key, false)
	createdRefs := []EntityRef{}
	for _, c := range q.snap.Services {
		if !c.Template && q.createdFrom(c.Key, s.Key) {
			createdRefs = append(createdRefs, serviceEntityRef(c))
		}
	}
	sortEntityRefs(createdRefs)
	d := &TemplateDetail{
		Plugins:                append([]contract.TemplatePlugin{}, rev.Contract.Service.Template.Plugins...),
		Requirements:           offeringUsesPreview(reqs),
		UnresolvedRequirements: unresolvedDependencyItems(serviceEntityRef(s), edges),
		CreatedServices:        refPreview(createdRefs),
		HiddenCreatedServices:  s.HiddenCreated,
		HiddenRequirements:     len(hidden),
		SignatureNote:          selfDeclaredSignature,
		Provenance:             ProvenanceDeclared,
	}
	if rev.Provenance != nil {
		sig := rev.Provenance.Signature
		d.Signature = &sig
	}
	d.Instantiate, d.InstantiateReason = instantiation(rev)
	return d
}

// revisionSignature is r's signature check, unknown when no source that read
// it checks signatures.
func revisionSignature(r *ContractRevision) BundleSignature {
	if r.Provenance == nil {
		return BundleSignature{Status: SignatureUnknown, Reason: "no source that read this revision checks signatures"}
	}
	return r.Provenance.Signature
}

// instantiation is how to create a service from rev: only from its
// digest-pinned registry reference, so the copied command creates from the
// bytes shown, or from the directory a file:// reference names.
func instantiation(rev *ContractRevision) (*TemplateInstantiation, string) {
	ref := ""
	switch {
	case strings.HasPrefix(rev.ResolvedRef, "oci://") && strings.Contains(rev.ResolvedRef, "@sha256:"):
		ref = rev.ResolvedRef
	case strings.HasPrefix(rev.RequestedRef, "file://"):
		ref = strings.TrimPrefix(rev.RequestedRef, "file://")
	default:
		return nil, "no digest-pinned reference: publish the golden path to get a copyable command"
	}
	return &TemplateInstantiation{
		Ref: ref,
		CLI: "pacto init <name> --from " + ref + " --owner <team>",
		MCP: "pacto_create with from: " + ref + ", name and owner",
	}, ""
}

// originDetail is the declared origin of s, nil when it declares none.
func (q *Query) originDetail(s *ServiceRecord) *OriginDetail {
	o := s.Origin
	if o == nil {
		return nil
	}
	d := &OriginDetail{Ref: o.Ref, Digest: o.Digest, Provenance: ProvenanceDeclared, Reason: o.Reason}
	if o.GoldenPath != "" {
		d.GoldenPath = new(q.serviceRef(o.GoldenPath))
	}
	if r := q.snap.Revisions[o.Revision]; r != nil {
		d.Revision = new(revisionEntityRef(r))
	}
	return d
}

// creationChecklist derives the creation steps of a service with an origin
// from what the fleet holds, nil for any other service. A step is done only on
// its signal; the golden path's signature never is (only creation checks it
// against a trust config).
func (q *Query) creationChecklist(s *ServiceRecord, view *ServiceView, consumed []OfferingUse) []CreationStep {
	if s.Origin == nil {
		return nil
	}
	path := q.snap.Revisions[s.Origin.Revision]
	steps := []CreationStep{pathSignatureStep(path)}
	// view holds detached copies; validated lives on the snapshot record.
	newest := q.snap.Revisions[newestInUse(nil, view.Revisions).Key]
	steps = append(steps,
		CreationStep{Step: StepContract, Status: StepStatusDone, Detail: "revision " + newest.Version + " read from " + newest.Source},
		validationStep(newest))
	if path != nil && path.Contract.Service.Template != nil {
		for _, p := range path.Contract.Service.Template.Plugins {
			steps = append(steps, CreationStep{Step: StepPlugin, Name: p.Name, Status: StepStatusUnknown, Detail: "Pacto does not observe what a plugin wrote"})
		}
	}
	return append(steps, publishedStep(view.Revisions), q.fleetStep(view), deployedStep(view.Targets), provisioningStep(consumed))
}

// pathSignatureStep is the signature step of the golden path revision path.
func pathSignatureStep(path *ContractRevision) CreationStep {
	st := CreationStep{Step: StepSignature, Status: StepStatusUnknown}
	if path == nil {
		st.Detail = "the golden path is not in this snapshot"
		return st
	}
	sig := revisionSignature(path)
	switch sig.Status {
	case SignatureVerified:
		st.Detail = "signed by " + sig.Signer + " (" + sig.Issuer + "), the signer the golden path's own contract declares; Pacto did not check it against a trust config"
	case SignatureUnverified:
		st.Status, st.Detail = StepStatusFailed, "the golden path's signature was read and failed verification"
	case SignatureUnsigned:
		st.Detail = "the golden path is unsigned"
	default:
		st.Detail = cmp.Or(sig.Reason, "the golden path's signature was not checked")
	}
	return st
}

// validationStep is how the validation of r ended. A valid revision is done
// only when no reference was left unchecked, the same rule the creation-time
// step applies.
func validationStep(r *ContractRevision) CreationStep {
	switch {
	case !r.validated:
		return CreationStep{Step: StepValidation, Status: StepStatusUnknown, Detail: "the contract was not validated"}
	case !r.Valid:
		return CreationStep{Step: StepValidation, Status: StepStatusFailed, Detail: "revision " + r.Version + " is invalid"}
	}
	var codes []string
	for _, f := range r.Validation {
		if c := string(f.Code); strings.HasSuffix(c, "_UNVERIFIED") || strings.HasSuffix(c, "_NOT_ENFORCED") {
			codes = append(codes, c)
		}
	}
	if len(codes) == 0 {
		return CreationStep{Step: StepValidation, Status: StepStatusDone, Detail: "revision " + r.Version + " is valid"}
	}
	slices.Sort(codes)
	return CreationStep{Step: StepValidation, Status: StepStatusUnknown,
		Detail: "revision " + r.Version + " is valid, with references left unchecked: " + strings.Join(slices.Compact(codes), ", ")}
}

// publishedStep is done when a registry served a revision.
func publishedStep(revs []*ContractRevision) CreationStep {
	for _, r := range revs {
		for _, ref := range []string{r.ResolvedRef, r.RequestedRef} {
			if strings.HasPrefix(ref, "oci://") {
				return CreationStep{Step: StepPublished, Status: StepStatusDone, Detail: "read from " + ref}
			}
		}
	}
	return CreationStep{Step: StepPublished, Status: StepStatusUnknown, Detail: "seen only in local files"}
}

// fleetStep names the sources that saw the service.
func (q *Query) fleetStep(view *ServiceView) CreationStep {
	var ids []string
	for _, src := range q.serviceSources(view) {
		ids = append(ids, src.Label)
	}
	return CreationStep{Step: StepFleet, Status: StepStatusDone, Detail: "seen by " + strings.Join(ids, ", ")}
}

// deployedStep is done when the fleet observes an operational target.
func deployedStep(targets []*TargetRecord) CreationStep {
	switch len(targets) {
	case 0:
		return CreationStep{Step: StepDeployed, Status: StepStatusUnknown, Detail: "no operational target observed"}
	case 1:
		return CreationStep{Step: StepDeployed, Status: StepStatusDone, Detail: "1 operational target"}
	}
	return CreationStep{Step: StepDeployed, Status: StepStatusDone, Detail: strconv.Itoa(len(targets)) + " operational targets"}
}

// provisioningStep folds what providers report about the offerings the
// service consumes: any Failed fails it, all Ready makes it done.
func provisioningStep(consumed []OfferingUse) CreationStep {
	ready, reported, total := 0, 0, 0
	for _, u := range consumed {
		for _, p := range u.Provisioning {
			total++
			if p.ReportedPhase != "" {
				reported++
			}
			switch p.Phase {
			case string(evidence.PhaseFailed):
				return CreationStep{Step: StepProvisioning, Status: StepStatusFailed, Detail: "a provider reports " + u.Provider.Label + " failed to provision"}
			case string(evidence.PhaseReady):
				ready++
			}
		}
	}
	switch {
	case total == 0:
		return CreationStep{Step: StepProvisioning, Status: StepStatusNotApplicable, Detail: "the service consumes no offering that provisions"}
	case ready == total:
		return CreationStep{Step: StepProvisioning, Status: StepStatusDone, Detail: "every provider reports Ready"}
	case reported == 0:
		return CreationStep{Step: StepProvisioning, Status: StepStatusUnknown, Detail: "no provider has reported provisioning for what the service consumes"}
	}
	return CreationStep{Step: StepProvisioning, Status: StepStatusUnknown, Detail: strconv.Itoa(total-ready) + " of " + strconv.Itoa(total) + " provisioning statuses are not Ready"}
}
