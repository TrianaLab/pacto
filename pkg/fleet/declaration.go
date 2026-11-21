package fleet

import (
	"regexp"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// DeclarationSignerPolicy is the reader's installation signer policy: the
// issuers whose people may declare a service, each with the anchored regular
// expression a person's whole subject must match. Unread says why it could
// not be read; then no declaration's cluster facts are shown.
type DeclarationSignerPolicy struct {
	Signers []PolicySigner
	Unread  string
}

// PolicySigner is one issuer of a DeclarationSignerPolicy and the subjects
// it allows.
type PolicySigner struct {
	Issuer   string
	Subjects string
}

// noSignerPolicy is why a build given no signer policy shows no
// declaration's cluster facts.
const noSignerPolicy = "no installation signer policy was given to this reader"

// allows is why s may not declare under p, "" when it may: an unread or
// absent policy allows nobody, unknown is never positive.
func (p *DeclarationSignerPolicy) allows(s contract.InferredSigner) string {
	switch {
	case p == nil:
		return noSignerPolicy
	case p.Unread != "":
		return "the installation signer policy could not be read (" + p.Unread + ")"
	}
	i := slices.IndexFunc(p.Signers, func(ps PolicySigner) bool { return ps.Issuer == s.Issuer })
	if i < 0 {
		return "the installation signer policy lists no issuer " + s.Issuer
	}
	if re, err := regexp.Compile("^(?:" + p.Signers[i].Subjects + ")$"); err != nil || !re.MatchString(s.Subject) {
		return "the signer matches none of the subjects the installation signer policy allows for " + s.Issuer
	}
	return ""
}

// acceptDeclaration is the one place that decides whether the facts of a
// revision's inferred record count. A record with no declaration counts as
// read. A draft is not a version: the revision is not ingested (false). A
// declared record's declarer is accepted only when DeclarationAccepted
// accepts it; otherwise its cluster facts, coverage and hidden counts are
// dropped, its folder fields stay and a limitation says why. It records on
// rev whether the signature matched and, when the declarer is not accepted,
// why.
func acceptDeclaration(rev *ContractRevision, rec *contract.InferredRecord, signers *DeclarationSignerPolicy) (*contract.InferredRecord, []Limitation, bool) {
	var asker *contract.InferredSigner
	if rec != nil && rec.Declaration != nil {
		asker = &rec.Declaration.Asker
	}
	boundSigners(rev.Provenance, asker)
	if asker == nil {
		return rec, nil, true
	}
	_, lim, ok := DeclarationAccepted(rev.Provenance, rec, signers)
	if lim.Code == LimitationDeclarationDraftIgnored {
		return nil, []Limitation{{Code: lim.Code, Source: rev.Source,
			Message: "revision " + rev.Service + " is a declaration draft, not a version, so it is left out until a person accepts it"}}, false
	}
	rev.declarationChecked = lim.Code != LimitationDeclarationSignerUnverified
	rev.declarationRefused = lim.Message
	if ok || !hasClusterFacts(rec) {
		return rec, nil, true
	}
	kept, _ := withoutClusterFacts(rec)
	return kept, []Limitation{{Code: lim.Code, Source: rev.Source,
		Message: "revision " + rev.Service + " carries cluster facts that are not shown: " + lim.Message}}, true
}

// DeclarationAccepted is the one declaration predicate: whether the
// declarer of rec (which must carry a declaration) is accepted, a person
// whose signature prov verified and whom policy allows. When it is not, the
// limitation's code and message say why: a draft is never a version
// (LimitationDeclarationDraftIgnored), no verified signature names the asker
// as a person (LimitationDeclarationSignerUnverified), or policy does not
// allow the asker (LimitationDeclarationSignerNotAllowed). Its Source is
// left to the caller.
func DeclarationAccepted(prov *BundleProvenance, rec *contract.InferredRecord, policy *DeclarationSignerPolicy) (VerifiedSigner, Limitation, bool) {
	d := rec.Declaration
	switch {
	case d.State != contract.DeclarationDeclared:
		return VerifiedSigner{}, Limitation{Code: LimitationDeclarationDraftIgnored, Message: "a declaration draft is not a version"}, false
	case !signedBy(prov, d.Asker):
		return VerifiedSigner{}, Limitation{Code: LimitationDeclarationSignerUnverified, Message: "no signature this reader verified names its declarer as a person"}, false
	}
	if why := policy.allows(d.Asker); why != "" {
		return VerifiedSigner{}, Limitation{Code: LimitationDeclarationSignerNotAllowed, Message: why}, false
	}
	return VerifiedSigner{Subject: d.Asker.Subject, Issuer: d.Asker.Issuer, Class: contract.SignerClassHuman}, Limitation{}, true
}

// boundSigners keeps at most MaxProvenanceSigners of p's signers, in order,
// always keeping the one that matches asker: extra signatures anyone with
// push access attaches never push the declarer out of the list it is
// matched against.
func boundSigners(p *BundleProvenance, asker *contract.InferredSigner) {
	if p == nil || len(p.Signers) <= MaxProvenanceSigners {
		return
	}
	if asker != nil {
		if i := slices.IndexFunc(p.Signers, func(s VerifiedSigner) bool { return matches(s, *asker) }); i >= MaxProvenanceSigners {
			p.Signers[MaxProvenanceSigners-1] = p.Signers[i]
		}
	}
	p.Signers = p.Signers[:MaxProvenanceSigners]
}

// signedBy reports whether p lists a verified signature by asker, a person.
func signedBy(p *BundleProvenance, asker contract.InferredSigner) bool {
	return p != nil && slices.ContainsFunc(p.Signers, func(s VerifiedSigner) bool { return matches(s, asker) })
}

// matches reports whether s is asker, signing as a person.
func matches(s VerifiedSigner, asker contract.InferredSigner) bool {
	return s.Subject == asker.Subject && s.Issuer == asker.Issuer && s.Class == contract.SignerClassHuman
}

// clusterFact reports whether a record field or source was read from a cluster.
func clusterFact(source string) bool { return strings.HasPrefix(source, "cluster:") }

// hasClusterFacts reports whether rec carries anything read from a cluster.
func hasClusterFacts(rec *contract.InferredRecord) bool {
	return rec.Inputs.Cluster != nil || len(rec.Coverage) > 0 || rec.Hidden != nil ||
		slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return clusterFact(f.Source) })
}

// withoutClusterFacts is a copy of rec with no cluster-sourced field, cluster
// input, coverage or hidden count, and how many fields it dropped. rec is
// never changed: it may be shared by every view of the snapshot.
func withoutClusterFacts(rec *contract.InferredRecord) (*contract.InferredRecord, int) {
	c := *rec
	c.Fields = slices.DeleteFunc(slices.Clone(rec.Fields), func(f contract.Inferred) bool { return clusterFact(f.Source) })
	c.Inputs.Sources = slices.DeleteFunc(slices.Clone(rec.Inputs.Sources), func(s contract.InferredSourceFile) bool { return clusterFact(s.Path) })
	c.Inputs.Cluster, c.Coverage, c.Hidden = nil, nil, nil
	return &c, len(rec.Fields) - len(c.Fields)
}

// declarationScope is the namespace, workload kind and cluster of the
// inferred service rev's declaration came from, false when it has none. The
// cluster is the declaration's when s knows it, else "": a cluster id no
// source of s reported is a claim, so it never picks whose review applies.
func declarationScope(s *FleetSnapshot, rev *ContractRevision) (WorkloadScope, bool) {
	if rev.Record == nil || rev.Record.Declaration == nil {
		return WorkloadScope{}, false
	}
	d := rev.Record.Declaration
	_, ns, kind, _, ok := ParseK8sCaller(d.InferredFrom)
	cluster := ""
	if slices.ContainsFunc(s.Clusters, func(c ClusterRecord) bool { return c.ID == d.Cluster }) {
		cluster = d.Cluster
	}
	return WorkloadScope{ns, kind, cluster}, ok
}

// declarationSourceHidden reports whether hidden says no to the workload rev's
// declaration came from: its cluster facts are that workload's data, so they
// need namespace read and workload read of its kind. An inferredFrom Pacto
// cannot read hides them too: unknown is never positive.
func declarationSourceHidden(s *FleetSnapshot, rev *ContractRevision, hidden func(cluster, namespace, kind string) bool) bool {
	sc, ok := declarationScope(s, rev)
	if !ok {
		return rev.Record != nil && rev.Record.Declaration != nil && rev.Record.Declaration.InferredFrom != ""
	}
	return hidden(sc.Cluster, sc.Namespace, sc.Kind)
}

// hideClusterDependencies is a clone of rev without the cluster-sourced
// dependencies[<name>] fields (the dependency and its compatibility) that
// resolve to a service in hiddenSvc, counted as hidden cluster fields; nil
// when it has none. The fleet's versions of a hidden service never reach a
// viewer through another service's cluster read.
func hideClusterDependencies(s *FleetSnapshot, rev *ContractRevision, hiddenSvc map[ServiceKey]bool) *ContractRevision {
	if rev.Record == nil || rev.Record.Declaration == nil || len(hiddenSvc) == 0 {
		return nil
	}
	hides := func(f contract.Inferred) bool {
		rest, ok := strings.CutPrefix(f.Field, "dependencies[")
		name, _, closed := strings.Cut(rest, "]")
		if !ok || !closed || !clusterFact(f.Source) {
			return false
		}
		to, resolved := resolveDepService(s, rev.Domain, contract.Dependency{Name: name})
		return resolved && hiddenSvc[to]
	}
	if !slices.ContainsFunc(rev.Record.Fields, hides) {
		return nil
	}
	c, rec := *rev, *rev.Record
	rec.Fields = slices.DeleteFunc(slices.Clone(rev.Record.Fields), hides)
	c.Record, c.hiddenClusterFields = &rec, len(rev.Record.Fields)-len(rec.Fields)
	return &c
}

// hideDeclarationSource is a clone of rev for a viewer who may not read the
// workload its declaration came from: no inferredFrom, no cluster field,
// coverage or hidden count, only how many cluster fields were hidden. The
// snapshot's revision and record are never changed.
func hideDeclarationSource(rev *ContractRevision) *ContractRevision {
	c := *rev
	c.Record, c.hiddenClusterFields = withoutClusterFacts(rev.Record)
	d := *rev.Record.Declaration
	d.InferredFrom = ""
	c.Record.Declaration = &d
	c.declarationHidden = true
	return &c
}

// InferredCoverageRow is how completely one cluster source was read for a
// declared revision's inferred record.
type InferredCoverageRow struct {
	Source string `json:"source"`
	State  string `json:"state" enum:"complete,partial,failed"`
	Reason string `json:"reason,omitempty"`
}

// InferredHiddenCounts counts what a declared revision's cluster read left
// out: the services and callers the reader's permissions hid when the record
// was written, and the cluster fields this viewer may not see.
type InferredHiddenCounts struct {
	Services int `json:"services,omitempty"`
	Callers  int `json:"callers,omitempty"`
	Fields   int `json:"fields,omitempty" doc:"Cluster fields left out because you may not read the workload the declaration came from or the service they name"`
}

// DeclarationView is who declared a revision from an inferred service and
// what they accepted. Signer is set only when the declarer is accepted: a
// signature this reader verified names the declarer as a person
// (SignatureChecked) and the reader's signer policy allows them (NotAccepted
// is empty). Otherwise ClaimedSubject is the subject the record claims, for
// a tooltip only, and NotAccepted says why its cluster facts are not shown.
type DeclarationView struct {
	State            string          `json:"state" enum:"declared,draft"`
	Signer           *VerifiedSigner `json:"signer,omitempty"`
	ClaimedSubject   string          `json:"claimedSubject,omitempty" doc:"The declarer the record claims; set only when the declarer is not accepted"`
	SignatureChecked bool            `json:"signatureChecked" doc:"A signature this reader verified names the declarer as a person"`
	NotAccepted      string          `json:"notAccepted,omitempty" doc:"Why this reader does not accept the declarer, so its cluster facts are not shown; empty when it is accepted"`
	InferredFrom     string          `json:"inferredFrom,omitempty" doc:"The inferred service the declaration came from, k8s:<namespace>/<kind>/<name>"`
	// InferredFromHidden says the viewer may not read the workload the
	// declaration came from, so InferredFrom and every cluster fact are left out.
	InferredFromHidden bool     `json:"inferredFromHidden,omitempty" doc:"You may not read the workload the declaration came from, so its cluster facts are left out"`
	Cluster            string   `json:"cluster,omitempty" doc:"The uid of the kube-system namespace of the cluster read"`
	Baseline           string   `json:"baseline,omitempty" doc:"The published version the declaration started from"`
	Accepted           []string `json:"accepted,omitempty" doc:"The proposed ranges the declarer accepted, as <field> <range>"`
	Draft              string   `json:"draft,omitempty" doc:"The draft tag the declaration accepted"`
}

// declarationDetail is the coverage, hidden counts and declaration view of
// rev's inferred record, each nil when it has none.
func declarationDetail(rev *ContractRevision) ([]InferredCoverageRow, *InferredHiddenCounts, *DeclarationView) {
	rec := rev.Record
	if rec == nil || rec.Declaration == nil {
		return nil, nil, nil
	}
	var cov []InferredCoverageRow
	for _, c := range rec.Coverage {
		cov = append(cov, InferredCoverageRow(c))
	}
	hidden := InferredHiddenCounts{Fields: rev.hiddenClusterFields}
	if rec.Hidden != nil {
		hidden.Services, hidden.Callers = rec.Hidden.Services, rec.Hidden.Callers
	}
	var hp *InferredHiddenCounts
	if hidden != (InferredHiddenCounts{}) {
		hp = &hidden
	}
	d := rec.Declaration
	v := &DeclarationView{State: d.State, SignatureChecked: rev.declarationChecked, InferredFrom: d.InferredFrom,
		InferredFromHidden: rev.declarationHidden, Cluster: d.Cluster, NotAccepted: rev.declarationRefused}
	if v.SignatureChecked && v.NotAccepted == "" {
		v.Signer = &VerifiedSigner{Subject: d.Asker.Subject, Issuer: d.Asker.Issuer, Class: d.Asker.Class}
	} else {
		v.ClaimedSubject = d.Asker.Subject
	}
	if d.Baseline != nil {
		v.Baseline = d.Baseline.Version
	}
	if d.Draft != nil {
		v.Draft = d.Draft.Tag
	}
	for _, a := range d.Accepted {
		v.Accepted = append(v.Accepted, a.Field+" "+a.Range)
	}
	return cov, hp, v
}
