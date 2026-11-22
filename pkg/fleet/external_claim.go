package fleet

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalid"
)

// routedToApprovers is how an unclaimed external system is routed: a fixed
// phrase, never a rule. The fleet cannot enumerate approvers and grants
// nothing through it.
const routedToApprovers = "routed to the installation's default approvers (routing only; it grants no right)"

// claimer is one service whose history establishes a claim on key: rev is
// its newest revision, by the person whose declaration established it.
type claimer struct {
	key ExternalKey
	rev *ContractRevision
	by  string
}

// linkExternalClaims links each external system to the one service that
// claims it. A claim is established only by an accepted person declaration
// of inferredFrom external:<identity> in a revision that names the same
// service.external outside pacto-inferred: only that path ran the owner rule
// for a first owner. Later revisions of the service carry it while the
// newest keeps service.external; a newer unreadable record retracts it.
// Anything less links nothing and says why: an unverified declaration, two
// claimers, or a service.external no declaration backs (a CI publish never
// claims and never makes a claim ambiguous). Unknown is never positive.
func linkExternalClaims(snap *FleetSnapshot) {
	byKey := map[ExternalKey][]claimer{}
	for _, k := range slices.Sorted(maps.Keys(snap.Services)) {
		if c, ok := claimCandidate(snap, snap.Services[k]); ok {
			byKey[c.key] = append(byKey[c.key], c)
		}
	}
	q := &Query{snap: snap}
	for _, key := range slices.Sorted(maps.Keys(byKey)) {
		cs, rec := byKey[key], snap.Externals[key]
		if len(cs) > 1 {
			svcs := make([]ServiceKey, len(cs))
			for i, c := range cs {
				svcs[i] = c.rev.ServiceKey
			}
			snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationInferredLinkIgnored, Source: "fleet",
				Message: "external " + string(key) + " is claimed by " + keyList(svcs) + ", so it stays unclaimed until one claim is retracted"})
			continue
		}
		if rec == nil {
			continue // no consumer and no inferred contract names it
		}
		c := cs[0]
		b, _ := q.ownershipBucket(snap.Services[c.rev.ServiceKey])
		rec.Claim = &ExternalClaim{Service: c.rev.ServiceKey, Ref: claimRef(c.rev), Version: c.rev.Version, By: c.by,
			ownerless: b == OwnershipUnowned || b == OwnershipUnknown}
	}
}

// claimCandidate is the claim s establishes, false when it establishes none.
// It adds the limitation that says why a claim s attempts does not count.
func claimCandidate(snap *FleetSnapshot, s *ServiceRecord) (claimer, bool) {
	newest, decl, unreadable := claimRevisions(snap, s)
	if newest == nil {
		return claimer{}, false
	}
	key, names := externalOf(newest)
	id, claims := "", false
	if decl != nil {
		id, claims = contract.InferredFromExternal(decl.Record.Declaration.InferredFrom)
	}
	switch {
	case !claims:
		if names {
			snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationInferredLinkIgnored, Source: newest.Source,
				Message: string(s.Key) + " declares external " + string(key) + " without a claim: run pacto claim " + string(key) + " --owner <team>"})
		}
		return claimer{}, false
	case unreadable != nil && LessRevisionChrono(decl, unreadable):
		snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationInferredLinkIgnored, Source: unreadable.Source,
			Message: "the newest revision " + unreadable.Service + "@" + unreadable.Version + " ships an inferred record Pacto cannot read, so the claim of " +
				string(s.Key) + " on external " + id + " is not trusted"})
		return claimer{}, false
	}
	declKey, declNames := externalOf(decl)
	if !declNames || string(declKey) != id || !names || key != declKey {
		return claimer{}, false // retracted, or a declaration that names another system (validation refuses it)
	}
	by, why := claimTrusted(decl)
	if why != "" {
		snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationInferredLinkIgnored, Source: decl.Source,
			Message: string(s.Key) + " claims external " + id + ", but " + why + ", so it stays unclaimed"})
		return claimer{}, false
	}
	return claimer{key: key, rev: newest, by: by}, true
}

// claimRevisions is the newest revision of s, its newest declared one and
// its newest one whose inferred record Pacto cannot read; each nil when none.
func claimRevisions(snap *FleetSnapshot, s *ServiceRecord) (newest, decl, unreadable *ContractRevision) {
	newer := func(cur, rev *ContractRevision) bool { return cur == nil || LessRevisionChrono(cur, rev) }
	for _, rk := range s.Revisions {
		rev := snap.Revisions[rk]
		if newer(newest, rev) {
			newest = rev
		}
		if rev.recordUnreadable && newer(unreadable, rev) {
			unreadable = rev
		}
		if declarationOf(rev) != nil && newer(decl, rev) {
			decl = rev
		}
	}
	return newest, decl, unreadable
}

// declarationOf is rev's declared (not draft) declaration, nil when it has
// none.
func declarationOf(rev *ContractRevision) *contract.InferredDeclaration {
	if rev.Record == nil || rev.Record.Declaration == nil || rev.Record.Declaration.State != contract.DeclarationDeclared {
		return nil
	}
	return rev.Record.Declaration
}

// externalOf is the canonical identity rev's service.external names, false
// when it names none. A revision under pacto-inferred never gets here: it is
// diverted before it is a service (see divertInferredProvider).
func externalOf(rev *ContractRevision) (ExternalKey, bool) {
	if rev.Contract == nil || rev.Contract.Service.External == "" {
		return "", false
	}
	id, err := externalid.Parse(rev.Contract.Service.External)
	return ExternalKey(id), err == nil
}

// claimTrusted is the person whose declaration rev carries, or why it does
// not establish a claim: the declaration rule of a workload hand-over, and
// never an operator's signature.
func claimTrusted(rev *ContractRevision) (by, why string) {
	if operatorSigned(rev) {
		return "", "only this installation's operator signed it"
	}
	if why, fix := declarationRejected(rev); why != "" {
		return "", why + "; " + fix
	}
	return rev.Record.Declaration.Asker.Subject, ""
}

// claimRef is rev as repository@digest, "" when no registry ref names it.
func claimRef(rev *ContractRevision) string {
	repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(rev.ResolvedRef))
	if repo == "" {
		return ""
	}
	return repo + "@" + rev.Digest
}

// ClaimedRepository is the repository of the contract that claims r, "" when
// it is unclaimed or the viewer may not read the claimer.
func (r *ExternalRecord) ClaimedRepository() string {
	if r == nil || r.Claim == nil || r.Claim.Hidden {
		return ""
	}
	repo, _, _ := strings.Cut(r.Claim.Ref, "@")
	return repo
}

// unowned reports whether r has no owner: no claim, or a claimer with no
// verified owner (computed on the full snapshot, so a hidden claimer keeps
// it).
func (r *ExternalRecord) unowned() bool { return r.Claim == nil || r.Claim.ownerless }

// claimAction is the action that claims external id, nil when id is no
// canonical identity or names other egress.
func claimAction(id string) *NextAction {
	if p, err := externalid.Parse(id); err != nil || string(p) != id || ExternalKey(p) == OtherEgress {
		return nil
	}
	return &NextAction{Verb: "claim", Tool: "pacto_declare", CLI: "pacto claim " + id + " --owner <team>"}
}

// unownedExternalAttention reports one UNOWNED_EXTERNAL item per external
// system with consumers and no owner, ranked by how many services use it. It
// is routed to the installation's default approvers for routing only. An
// external is no service, so a scoped (per-service) answer holds none.
func (q *Query) unownedExternalAttention() []AttentionItem {
	if q.scope != nil {
		return nil
	}
	var out []AttentionItem
	for _, k := range slices.Sorted(maps.Keys(q.snap.Externals)) {
		rec := q.snap.Externals[k]
		n := rec.ConsumersTotal + rec.HiddenConsumers
		if n == 0 || rec.Class == ClassOtherEgress || !rec.unowned() {
			continue
		}
		label := rec.Label + ": " + strconv.Itoa(n) + " services use it"
		if n == 1 {
			label = rec.Label + ": 1 service uses it"
		}
		reason := "no declared contract claims it"
		if rec.Claim != nil {
			reason = "the service that claims it has no verified owner"
		}
		out = append(out, AttentionItem{
			Entity: externalEntityRef(rec), Label: label, rank: n,
			Severity: severityInfo, Code: "UNOWNED_EXTERNAL", Category: categoryUnowned,
			Summary:    "External system has no owner",
			Reason:     consumerWords(rec) + " " + rec.Label + ", and " + reason,
			NextStep:   "Claim " + string(k) + " for a team with pacto claim " + string(k) + " --owner <team>; until then it is " + routedToApprovers,
			NextAction: claimAction(string(k)),
		})
	}
	return out
}

// consumerWords names up to three visible consumers of rec and counts the
// rest, hidden ones without naming them: "a, b and 2 you cannot see use".
func consumerWords(rec *ExternalRecord) string {
	var names []string
	for _, c := range rec.Consumers {
		_, name := ParseServiceKey(c)
		names = append(names, name)
	}
	if len(names) > 3 {
		names = append(names[:3], strconv.Itoa(len(names)-3)+" more")
	}
	if rec.HiddenConsumers > 0 {
		names = append(names, strconv.Itoa(rec.HiddenConsumers)+" you cannot see")
	}
	verb := " use"
	if rec.ConsumersTotal+rec.HiddenConsumers == 1 {
		verb = " uses"
	}
	if len(names) < 2 {
		return strings.Join(names, "") + verb
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + verb
}

// claimReason is the explain reason of rec's claim, or of its absence.
func claimReason(rec *ExternalRecord) Reason {
	switch c := rec.Claim; {
	case c == nil:
		return Reason{Code: ReasonExternalUnclaimed, Message: "Unclaimed: " + routedToApprovers}
	case c.Hidden:
		return Reason{Code: ReasonExternalClaimed, Message: "Claimed by a service you may not read"}
	default:
		return Reason{Code: ReasonExternalClaimed, Message: "Claimed by " + string(c.Service) + " " + c.Version + " (declared by " + c.By + ")"}
	}
}
