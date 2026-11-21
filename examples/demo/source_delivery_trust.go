package main

import (
	"context"
	"slices"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// The promoter's registry watch, in miniature: every entry Stage that reads
// candidates.registry is judged by the real rule engine (delivery.JudgePublisher)
// against the rule the installation derives from delivery.publishers.registry.
// orders publishes from a release branch the rule does not cover, which bob
// pinned; audit-log's newest version is signed from a hotfix branch nobody
// confirmed yet, so a held trust operation asks a person.

// demoPublishers is the demo installation's delivery.publishers.
var demoPublishers = delivery.PublisherConfig{Registry: "ghcr.io/trianalab"}

// signers is the CI workflow and branch that signs each service's versions;
// every other service publishes from publish.yml on main, which the rule covers.
var signers = map[string][2]string{"orders": {"release.yml", "release"}, "audit-log": {"hotfix.yml", "hotfix"}}

// auditLogPending is the version audit-log's watch read and could not accept.
var auditLogPending = story.ArtifactDigest("audit-log", "1.1.0")

// signerOf is the GitHub Actions identity that signed service's versions.
func signerOf(service string) delivery.PublisherIdentity {
	w := signers[service]
	if w[0] == "" {
		w = [2]string{"publish.yml", "main"}
	}
	rule, _ := demoPublishers.Rule()
	repo := "https://github.com/trianalab/" + service
	return delivery.Classified(delivery.PublisherIdentity{Issuer: rule.Issuer, Repository: repo, Ref: "refs/heads/" + w[1], CI: true,
		Subject: repo + "/.github/workflows/" + w[0] + "@refs/heads/" + w[1]}, nil)
}

// watchRegistry is the watch's first pass after the replay: bob's pin of
// orders, every registry Stage's status.publisher and audit-log's trust
// operation.
func (d *demoDelivery) watchRegistry() {
	at := stamp(d.clock())
	d.pins = map[string][]delivery.IdentityPin{}
	id := signerOf("orders")
	d.pins["orders"] = []delivery.IdentityPin{{Service: delivery.ServiceRef{Name: "orders"}, Namespace: story.Test, Issuer: id.Issuer, Subject: id.Subject,
		Repository: id.Repository, Approver: "bob@example.com", Approval: recordID("orders-trust-approval"), Operation: "op-orders-trust", At: at}}
	for _, s := range d.stages {
		if s.Spec.Candidates.Registry != nil {
			d.judge(s, at)
		}
	}
	d.raiseTrust(d.stage(story.Prod, "audit-log"), at)
}

// judge writes s's status.publisher: what the watch judges against and its
// verdicts on its published digests (and audit-log's pending one), newest
// first. status.published itself is the replay's.
func (d *demoDelivery) judge(s *delivery.StageObject, at string) {
	svc := s.Spec.Service
	rule, _ := demoPublishers.Rule()
	pins := pinsIn(d.pins[svc.Name], s.Metadata.Namespace)
	pins = pins[:min(1, len(pins))] // the newest is the one in force
	p := &delivery.PublisherStatus{Source: delivery.PublisherSourceRule, Rule: &rule, Pins: len(pins), CheckedAt: at}
	if len(pins) > 0 {
		p.PinAt, p.PinDigest = pins[0].At, recordID("pin "+svc.Name+" "+pins[0].At)
	}
	digests := slices.Clone(s.Status.Published)
	if svc.Name == "audit-log" {
		digests = append(digests, auditLogPending)
	}
	id := signerOf(svc.Name)
	for i := len(digests) - 1; i >= 0 && len(p.Verdicts) < delivery.MaxPublisherVerdicts; i-- {
		v := delivery.JudgePublisher(svc, id, id.Repository, []delivery.PublisherRule{rule}, delivery.PublisherSourceRule, pins, nil)
		v.Digest, v.Version, v.At = digests[i], cmpOr(versionOf(svc.Name, digests[i]), map[string]string{auditLogPending: "1.1.0"}[digests[i]]), at
		p.Verdicts = append(p.Verdicts, v)
	}
	s.Status.Publisher = p
}

// raiseTrust holds the trust operation the promoter raises for the newest
// new signer on s, planned by delivery.PlanFor like any other.
func (d *demoDelivery) raiseTrust(s *delivery.StageObject, at string) {
	i := slices.IndexFunc(s.Status.Publisher.Verdicts, func(v delivery.PublisherVerdict) bool { return v.Verdict == delivery.VerdictNewSigner })
	if i < 0 {
		return
	}
	v := s.Status.Publisher.Verdicts[i]
	spec := delivery.OperationSpec{Type: delivery.TypeTrust, Stage: s.Metadata.Name, Facet: delivery.FacetTrust, Reason: "new signer " + v.Identity.Subject,
		Params: delivery.Params{From: s.Status.Pointer, To: v.Digest, Publisher: v.Identity}, Actor: delivery.Actor{User: story.Promoter.User}}
	spec.IntentDigest = delivery.IntentDigest(spec, s.Spec)
	class, by := story.Policy.ClassifyBy(delivery.Identity{User: story.Promoter.User})
	p := delivery.PlanFor(context.Background(), delivery.PlanInput{Spec: spec, Stage: s.Spec, Status: s.Status, Class: class, RequireOwnerTeam: story.Policy.RequireOwnerTeam})
	o := &delivery.OperationObject{Metadata: delivery.ObjectMeta{Namespace: s.Metadata.Namespace, Name: delivery.TrustOperationName(s.Metadata.Name, *v.Identity),
		UID: "op-trust-" + s.Metadata.Name, CreationTimestamp: at}, Spec: spec}
	o.Status.ActorClass, o.Status.ActorClassifiedBy = class, by
	o.Status.Plan, o.Status.Times.Admitted, o.Status.Phase, o.Status.Question = &p, at, delivery.PhaseHeld, p.Question
	d.ops = append(d.ops, o)
}

// confirmSigner is the promoter consuming a person's approval of a trust
// operation: it concludes moving nothing, the recorder pins the identity
// and the watch's next pass accepts what that signer signed.
func (d *demoDelivery) confirmSigner(o *delivery.OperationObject, c delivery.Caller, approval string) {
	st, id := d.stage(o.Metadata.Namespace, o.Spec.Stage), o.Spec.Params.Publisher
	o.Status.Reason = "SignerConfirmed: a person confirmed that " + id.Subject + " may publish " + st.Spec.Service.Name
	d.conclude(o, delivery.Succeeded, delivery.Unverified)
	svc := st.Spec.Service
	pin := delivery.IdentityPin{Service: svc, Namespace: o.Metadata.Namespace, Issuer: id.Issuer, Subject: id.Subject, Repository: id.Repository,
		Approver: c.User, Approval: recordID(approval), Operation: o.Metadata.UID, At: o.Status.Times.Finished}
	d.pins[svc.Name] = append([]delivery.IdentityPin{pin}, d.pins[svc.Name]...)
	// ponytail: audit-log has no story contract, so its accepted digest is
	// not published here (no version could be read); the real watch publishes it.
	d.judge(st, o.Status.Times.Finished)
}

// pinsIn are the pins the Stages of namespace ns use, newest first: the
// first is the one in force.
func pinsIn(pins []delivery.IdentityPin, ns string) []delivery.IdentityPin {
	return slices.DeleteFunc(slices.Clone(pins), func(p delivery.IdentityPin) bool { return p.Namespace != ns })
}
