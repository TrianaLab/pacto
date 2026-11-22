package app

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/semver"
)

// carryBase is the published baseline a row is judged against: its bundle
// repository, version tag, manifest digest and bundle.
type carryBase struct {
	repo, version, digest string
	b                     *contract.Bundle
}

// carryResult is what carryForward did: the bundle to judge and publish
// (local itself when nothing is carried), the acceptances and the
// declaration it cites, how many fields acceptances (Pinned) and the
// declaration (Declared) pinned, each field a decision named (Carried) and
// what it says about the rest.
type carryResult struct {
	Bundle      *contract.Bundle
	Cited       []contract.CitedAcceptance
	Declaration *contract.CitedDeclaration
	Pinned      int
	Declared    int
	Carried     []ci.Carried
	Notes       []string
}

// carryForward folds the registry decisions the baseline carries into local
// (#452 decision 9): the counted acceptances attached to the baseline and
// the ones its record already cites, re-read by digest and re-judged, so a
// record that stops verifying stops carrying, and the registry declaration
// dc names (nil for none). Each decided field is pinned into local's
// pacto.yaml unless the repository's pacto.yaml declares that key (the
// repository wins); between acceptances the latest wins, and between an
// acceptance and the declaration the one on the newer registry revision
// (an acceptance at or above the declaration's version). Every decision
// that carried something is cited in the record, whose field list loses
// the pinned fields. A cited record that cannot be read is an error:
// dropping it would publish the decision away.
func carryForward(ctx context.Context, st *acceptanceStore, base carryBase, local *contract.Bundle, dc *declarationCarry) (carryResult, error) {
	res := carryResult{Bundle: local}
	service := string(fleet.NewServiceKeyDomain(contract.OCIDomain(base.repo), local.Contract.Service.Name))
	js, notes, err := carried(ctx, st, base, service)
	if err != nil {
		return res, err
	}
	res.Notes = notes
	rec, _ := contract.ReadInferredRecord(local.FS) // completion just wrote and verified it
	raw, c := local.RawYAML, *local.Contract
	c.Dependencies = slices.Clone(c.Dependencies)
	cands := dc.outranking(js)
	pinned := pinAccepted(js, &raw, c.Dependencies, &res, declaredFields(cands))
	fields := mergeCarry(&raw, &c, rec, cands, &res)
	if len(res.Cited) == 0 && len(fields) == 0 {
		return res, nil
	}
	if len(res.Cited) > 0 && st.note == publicGoodNote {
		res.Notes = append(res.Notes, publicGoodNote) // what carried was judged without the installation's issuers
	}
	if rec == nil {
		rec = &contract.InferredRecord{Format: contract.InferredRecordFormat}
	}
	for _, f := range fields {
		pinned[f] = true
		if name, ok := wholeEntry(f); ok {
			pinned["dependencies["+name+"].compatibility"] = true
		}
	}
	rec.Fields = slices.DeleteFunc(slices.Clone(rec.Fields), func(f contract.Inferred) bool { return pinned[f.Field] })
	rec.Inputs.Acceptances = res.Cited
	if len(fields) > 0 {
		slices.Sort(fields)
		res.Declaration = &contract.CitedDeclaration{Record: dc.d.Ref, Signer: citedSigner(dc.d.Signer), Fields: strings.Join(fields, ",")}
		rec.Inputs.Declaration = res.Declaration
	}
	fsys, err := copyFSWithReplace(local.FS, DefaultContractPath, raw)
	if err != nil {
		return res, fmt.Errorf("carrying registry decisions into pacto.yaml: %w", err)
	}
	res.Bundle = &contract.Bundle{Contract: &c, RawYAML: raw, FS: contract.WithInferredRecord(fsys, rec.Marshal()), Inferred: local.Inferred}
	return res, nil
}

// carried are the carryable acceptances of the baseline, oldest first: the
// ones its record cites, then the ones attached to it. A listing that
// fails carries nothing new, with a note; a cited record that cannot be
// read is an error. The public-good note is left to carryForward, which
// adds it only when something carries.
func carried(ctx context.Context, st *acceptanceStore, base carryBase, service string) ([]delivery.JudgedAcceptance, []string, error) {
	rec, _ := contract.ReadInferredRecord(base.b.FS) // an unreadable record cites nothing
	var js []delivery.JudgedAcceptance
	var notes []string
	if rec != nil {
		for _, c := range rec.Inputs.Acceptances {
			j, why, err := st.readCited(ctx, c.Record, hostOf(base.repo), service, nil)
			if err != nil {
				return nil, nil, fmt.Errorf("the acceptance %s %s cites: %w", c.Record, base.version, err)
			}
			if why != "" {
				notes = append(notes, "acceptance "+c.Record+" no longer carried: "+why)
				continue
			}
			js = append(js, j)
		}
	}
	attached, more, err := st.readAcceptances(ctx, acceptanceQuery{Subject: base.repo + "@" + base.digest, Repo: base.repo, Service: service,
		Version: base.version, Owner: declaredOwner(base.b.Contract, rec), Contract: base.b.Contract, Record: rec, Wants: func(impact.Acceptance) bool { return false }})
	if err != nil {
		notes = append(notes, "acceptances of "+base.version+" not read, none carried: "+err.Error())
	}
	notes = append(notes, slices.DeleteFunc(more, func(n string) bool { return n == publicGoodNote })...)
	for _, j := range attached {
		if !slices.ContainsFunc(js, func(c delivery.JudgedAcceptance) bool { return c.Ref == j.Ref }) {
			js = append(js, j)
		}
	}
	js = slices.DeleteFunc(js, func(j delivery.JudgedAcceptance) bool { return !carryable(j) })
	slices.SortStableFunc(js, func(a, b delivery.JudgedAcceptance) int { return a.SignedAt.Compare(b.SignedAt) })
	return js, notes, nil
}

// carryable reports whether j is carried: counted, or a break the owner
// accepted, whose expiry the gate judges against the announcement (the
// carry reads none). A routed or proposal acceptance never carries.
func carryable(j delivery.JudgedAcceptance) bool {
	return j.State == impact.AcceptCounted || j.State == impact.AcceptExpired && j.Record.Kind == delivery.AcceptKindBreak
}

// pinAccepted pins the latest accepted value of each range field of js
// that no declaration outranks (skip) into raw and deps, cites every record
// that carried something in res, records each field it decided and returns
// the pinned fields.
func pinAccepted(js []delivery.JudgedAcceptance, raw *[]byte, deps []contract.Dependency, res *carryResult, skip map[string]bool) map[string]bool {
	latest := map[string]int{}
	for i, j := range js {
		for _, r := range j.Record.Ranges {
			latest[r.Field] = i
		}
	}
	pinned := map[string]bool{}
	for i, j := range js {
		var fields []string
		if b := j.Record.Break; b != nil {
			fields = append(fields, "break "+b.Provider+"@"+b.Version)
		}
		for _, r := range j.Record.Ranges {
			if latest[r.Field] != i || skip[r.Field] {
				continue
			}
			rc := ci.Carried{Field: r.Field, Value: r.Value, From: carriedFromRegistry, Kind: carriedKindAccepted, Record: j.Ref, Signer: j.Signer, State: ci.CarriedCarried}
			if why := pinField(raw, deps, r); why != "" {
				rc.Value, rc.From, rc.State, rc.Reason = declaredValue(deps, r.Field), carriedFromPactoYAML, ci.CarriedOverridden, "registry accepts "+r.Value
				if why != "pacto.yaml declares "+r.Field {
					rc.Value = r.Value
					rc.From, rc.State, rc.Reason = carriedFromRegistry, ci.CarriedDropped, why
				}
				res.Carried = append(res.Carried, rc)
				res.Notes = append(res.Notes, why+"; acceptance "+j.Ref+" not applied")
				continue
			}
			res.Carried = append(res.Carried, rc)
			fields, pinned[r.Field] = append(fields, r.Field), true
		}
		if len(fields) > 0 {
			res.Cited = append(res.Cited, contract.CitedAcceptance{Record: j.Ref, Signer: j.Signer, Fields: strings.Join(fields, ", ")})
		}
	}
	res.Pinned = len(pinned)
	return pinned
}

// pinField pins one accepted field into raw and deps; why it was not
// pinned otherwise.
func pinField(raw *[]byte, deps []contract.Dependency, r delivery.AcceptedRange) string {
	name, key, ok := dependencyField(r.Field)
	if !ok {
		return r.Field + " is no dependency ref or compatibility"
	}
	out, err := contract.PinDependencyField(*raw, name, key, r.Value)
	switch {
	case err != nil:
		return err.Error()
	case bytes.Equal(out, *raw):
		return "pacto.yaml declares " + r.Field
	case key == "ref":
		// A dependency with a ref needs a compatibility: unknown until a
		// range is accepted (a declared one stays).
		out, _ = contract.PinDependencyField(out, name, "compatibility", contract.CompatibilityUnknown)
	}
	*raw = out
	for k := range deps {
		if deps[k].Name != name {
			continue
		}
		if key == "ref" {
			deps[k].Ref = r.Value
		} else {
			deps[k].Compatibility = r.Value
		}
	}
	return ""
}

// declaredValue is the value deps holds for the range field, as pacto.yaml
// writes it.
func declaredValue(deps []contract.Dependency, field string) string {
	name, key, _ := dependencyField(field)
	for _, d := range deps {
		if d.Name != name {
			continue
		}
		if key == "ref" {
			return d.Ref
		}
		return d.Compatibility
	}
	return ""
}

// dependencyField splits dependencies[<name>].<key> for key ref or
// compatibility.
func dependencyField(field string) (name, key string, ok bool) {
	rest, found := strings.CutPrefix(field, "dependencies[")
	name, key, _ = strings.Cut(rest, "].")
	return name, key, found && name != "" && (key == "ref" || key == "compatibility")
}

// Where a Carried field's published value comes from, and the kind of
// decision that named it.
const (
	carriedFromRegistry  = "registry"
	carriedFromPactoYAML = "pacto.yaml"
	carriedKindDeclared  = "declared"
	carriedKindAccepted  = "accepted"
)

// carryCandidate is one field a registry decision names: the record key,
// the decided value, the decision kind, record and signer, and the registry
// revision the decision belongs to (AtVersion). owner and entry hold the
// owner block or dependency entry it pins; dropped says why it never
// carries ("" when it may).
type carryCandidate struct {
	Field, Value, Kind, Record, Signer, AtVersion string
	owner                                         *contract.Owner
	entry                                         *contract.Dependency
	dropped                                       string
}

// declarationCarry is a counted registry declaration and the fields it
// names, owner rule and owner match applied (declarationCandidates).
type declarationCarry struct {
	d     *registryDeclaration
	cands []carryCandidate
}

// outranking are dc's candidates no acceptance in js outranks: an
// acceptance of the same field on a registry revision at or above the
// declaration's version wins (Decision 1: revisions, never signing times).
func (dc *declarationCarry) outranking(js []delivery.JudgedAcceptance) []carryCandidate {
	if dc == nil {
		return nil
	}
	return slices.DeleteFunc(slices.Clone(dc.cands), func(c carryCandidate) bool {
		return slices.ContainsFunc(js, func(j delivery.JudgedAcceptance) bool {
			return semver.Compare(j.Record.Version, c.AtVersion) >= 0 && slices.ContainsFunc(j.Record.Ranges, func(r delivery.AcceptedRange) bool { return r.Field == c.Field })
		})
	})
}

// declaredFields are the range fields cands decide, which acceptances then
// leave alone.
func declaredFields(cands []carryCandidate) map[string]bool {
	out := map[string]bool{}
	for _, c := range cands {
		out[c.Field] = true
	}
	return out
}

// mergeCarry pins each declaration candidate into raw and c unless the
// repository's pacto.yaml writes its key, records what it decided in res
// and returns the record keys it carried. A range on a dependency
// pacto.yaml does not name adds the whole entry, only while rec still
// infers it: a dependency the service stopped calling is never re-added.
func mergeCarry(raw *[]byte, c *contract.Contract, rec *contract.InferredRecord, cands []carryCandidate, res *carryResult) []string {
	var fields []string
	for _, cand := range cands {
		rc := ci.Carried{Field: cand.Field, Value: cand.Value, From: carriedFromRegistry, Kind: cand.Kind, Record: cand.Record, Signer: cand.Signer, State: ci.CarriedCarried}
		switch {
		case cand.dropped != "" || cand.owner == nil && cand.entry == nil:
			rc.State, rc.Reason = ci.CarriedDropped, cmp.Or(cand.dropped, "nothing to carry")
		case cand.owner != nil:
			rc = mergeOwner(raw, c, rec, cand, rc, res)
		default:
			rc = mergeRange(raw, c, rec, cand, rc)
		}
		switch rc.State {
		case ci.CarriedCarried:
			fields = append(fields, rc.Field)
		case ci.CarriedDropped:
			res.Notes = append(res.Notes, cand.Field+" from the registry declaration "+cand.Record+" not carried: "+rc.Reason)
		}
		res.Carried = append(res.Carried, rc)
	}
	res.Declared = len(fields)
	return fields
}

// mergeOwner pins the declared owner block unless pacto.yaml writes one,
// and notes an inferred owner that differs from it.
func mergeOwner(raw *[]byte, c *contract.Contract, rec *contract.InferredRecord, cand carryCandidate, rc ci.Carried, res *carryResult) ci.Carried {
	if contract.Declares(*raw, cand.Field) {
		rc.Value, rc.From, rc.State, rc.Reason = carriedOwner(c.Service.Owner), carriedFromPactoYAML, ci.CarriedOverridden, "registry declares "+cand.Value
		return rc
	}
	out, err := contract.PinServiceOwner(*raw, *cand.owner)
	if err != nil {
		rc.State, rc.Reason = ci.CarriedDropped, err.Error()
		return rc
	}
	*raw, c.Service.Owner = out, *cand.owner
	if rec != nil {
		for _, f := range rec.Fields {
			if f.Field == cand.Field && f.Value != cand.Value {
				res.Notes = append(res.Notes, "service.owner inferred "+f.Value+" from "+f.Source+" differs from the registry declaration")
			}
		}
	}
	return rc
}

// mergeRange pins a declared range: overridden when pacto.yaml writes it,
// pinned on a dependency pacto.yaml names, else the whole entry while rec
// still infers the dependency.
func mergeRange(raw *[]byte, c *contract.Contract, rec *contract.InferredRecord, cand carryCandidate, rc ci.Carried) ci.Carried {
	name := cand.entry.Name
	if contract.Declares(*raw, cand.Field) {
		rc.From, rc.State, rc.Reason = carriedFromPactoYAML, ci.CarriedOverridden, "registry accepts "+cand.Value
		if i := slices.IndexFunc(c.Dependencies, func(d contract.Dependency) bool { return d.Name == name }); i >= 0 {
			rc.Value = c.Dependencies[i].Compatibility
		}
		return rc
	}
	out, err := contract.PinDependencyField(*raw, name, "compatibility", cand.Value)
	if err == nil {
		*raw = out
		for k := range c.Dependencies {
			if c.Dependencies[k].Name == name {
				c.Dependencies[k].Compatibility = cand.Value
			}
		}
		return rc
	}
	whole := "dependencies[" + name + "]"
	if rec == nil || !slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == whole }) {
		rc.State, rc.Reason = ci.CarriedDropped, name+" is no longer discovered; write it in pacto.yaml to keep it"
		return rc
	}
	out, err = contract.AddDependency(*raw, *cand.entry)
	if err != nil {
		rc.State, rc.Reason = ci.CarriedDropped, err.Error()
		return rc
	}
	*raw, c.Dependencies = out, append(c.Dependencies, *cand.entry)
	rc.Field = whole
	if cand.entry.Ref != "" {
		rc.Value += " (" + cand.entry.Ref + ")"
	}
	return rc
}

// wholeEntry reports whether field is a whole dependency entry,
// dependencies[<name>], and its name.
func wholeEntry(field string) (string, bool) {
	rest, ok := strings.CutPrefix(field, "dependencies[")
	name, tail, _ := strings.Cut(rest, "]")
	return name, ok && tail == ""
}

// carriedOwner is how a Carried field names an owner: its team, else its DRI,
// else its first contact.
func carriedOwner(o contract.Owner) string {
	first := ""
	if len(o.Contacts) > 0 {
		first = o.Contacts[0].Value
	}
	return cmp.Or(o.Team, o.DRI, first)
}

// citedSigner names a verified signer as "<subject> (<issuer>)".
func citedSigner(s fleet.VerifiedSigner) string {
	return s.Subject + " (" + s.Issuer + ")"
}
