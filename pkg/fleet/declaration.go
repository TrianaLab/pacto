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
// declared record keeps its cluster facts only when a signature the
// revision's provenance verified names its asker as a person and the
// reader's signer policy allows that person; otherwise its cluster facts,
// coverage and hidden counts are dropped, its folder fields stay and a
// limitation says why. It records on rev whether the signer matched.
func acceptDeclaration(rev *ContractRevision, rec *contract.InferredRecord, signers *DeclarationSignerPolicy) (*contract.InferredRecord, []Limitation, bool) {
	if rec == nil || rec.Declaration == nil {
		return rec, nil, true
	}
	d := rec.Declaration
	if d.State != contract.DeclarationDeclared {
		return nil, []Limitation{{Code: LimitationDeclarationDraftIgnored, Source: rev.Source,
			Message: "revision " + rev.Service + " is a declaration draft, not a version, so it is left out until a person accepts it"}}, false
	}
	rev.declarationChecked = signedBy(rev.Provenance, d.Asker)
	if !hasClusterFacts(rec) {
		return rec, nil, true
	}
	code, why := LimitationDeclarationSignerUnverified, "no signature this reader verified names its declarer as a person"
	if rev.declarationChecked {
		no := signers.allows(d.Asker)
		if no == "" {
			return rec, nil, true
		}
		code, why = LimitationDeclarationSignerNotAllowed, no
	}
	kept, _ := withoutClusterFacts(rec)
	return kept, []Limitation{{Code: code, Source: rev.Source,
		Message: "revision " + rev.Service + " carries cluster facts that are not shown: " + why}}, true
}

// signedBy reports whether p lists a verified signature by asker, a person.
func signedBy(p *BundleProvenance, asker contract.InferredSigner) bool {
	return p != nil && slices.ContainsFunc(p.Signers, func(s VerifiedSigner) bool {
		return s.Subject == asker.Subject && s.Issuer == asker.Issuer && s.Class == contract.SignerClassHuman
	})
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

// declarationNamespace is the namespace of the inferred service rev's
// declaration came from, false when it has none.
func declarationNamespace(rev *ContractRevision) (string, bool) {
	if rev.Record == nil || rev.Record.Declaration == nil {
		return "", false
	}
	_, ns, _, _, ok := ParseK8sCaller(rev.Record.Declaration.InferredFrom)
	return ns, ok
}

// hideDeclarationSource is a clone of rev for a viewer who may not read the
// namespace its declaration came from: no inferredFrom, no cluster field,
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
	Fields   int `json:"fields,omitempty" doc:"Cluster fields left out because you may not read the namespace the declaration came from"`
}

// DeclarationView is who declared a revision from an inferred service and
// what they accepted. Signer is set only when SignatureChecked: a signature
// this reader verified names the declarer as a person. Otherwise
// ClaimedSubject is the unverified subject the record claims, for a tooltip
// only.
type DeclarationView struct {
	State            string          `json:"state" enum:"declared,draft"`
	Signer           *VerifiedSigner `json:"signer,omitempty"`
	ClaimedSubject   string          `json:"claimedSubject,omitempty" doc:"The declarer the record claims, unverified; set only when signatureChecked is false"`
	SignatureChecked bool            `json:"signatureChecked" doc:"A signature this reader verified names the declarer as a person"`
	InferredFrom     string          `json:"inferredFrom,omitempty" doc:"The inferred service the declaration came from, k8s:<namespace>/<kind>/<name>"`
	// InferredFromHidden says the viewer may not read the namespace the
	// declaration came from, so InferredFrom and every cluster fact are left out.
	InferredFromHidden bool     `json:"inferredFromHidden,omitempty" doc:"You may not read the namespace the declaration came from, so its cluster facts are left out"`
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
		InferredFromHidden: rev.declarationHidden, Cluster: d.Cluster}
	if v.SignatureChecked {
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
