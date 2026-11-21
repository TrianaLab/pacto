package app

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// carryBase is the published baseline a row is judged against: its bundle
// repository, version tag, manifest digest and bundle.
type carryBase struct {
	repo, version, digest string
	b                     *contract.Bundle
}

// carryResult is what carryForward did: the bundle to judge and publish
// (local itself when nothing is carried), the acceptances it cites, how
// many fields it pinned and what it says about the rest.
type carryResult struct {
	Bundle *contract.Bundle
	Cited  []contract.CitedAcceptance
	Pinned int
	Notes  []string
}

// carryForward folds the signed acceptances the baseline carries into
// local (#452 decision 9): the counted ones attached to the baseline and
// the ones its record already cites, re-read by digest and re-judged, so a
// record that stops verifying stops carrying. Each accepted range is
// pinned into local's pacto.yaml unless the repository's pacto.yaml
// declares that key (the repository wins); the latest acceptance of a
// field wins. Every record that carried something is cited in the record,
// whose field list loses the pinned fields. A cited record that cannot be
// read is an error: dropping it would publish the decision away.
func carryForward(ctx context.Context, st *acceptanceStore, base carryBase, local *contract.Bundle) (carryResult, error) {
	res := carryResult{Bundle: local}
	service := string(fleet.NewServiceKeyDomain(contract.OCIDomain(base.repo), local.Contract.Service.Name))
	js, notes, err := carried(ctx, st, base, service)
	if err != nil {
		return res, err
	}
	res.Notes = notes
	raw, deps := local.RawYAML, slices.Clone(local.Contract.Dependencies)
	pinned := pinAccepted(js, &raw, deps, &res)
	if len(res.Cited) == 0 {
		return res, nil
	}
	if st.note == publicGoodNote {
		res.Notes = append(res.Notes, publicGoodNote) // what carried was judged without the installation's issuers
	}
	rec, _ := contract.ReadInferredRecord(local.FS) // completion just wrote and verified it
	if rec == nil {
		rec = &contract.InferredRecord{Format: contract.InferredRecordFormat}
	}
	rec.Fields = slices.DeleteFunc(slices.Clone(rec.Fields), func(f contract.Inferred) bool { return pinned[f.Field] })
	rec.Inputs.Acceptances = res.Cited
	fsys, err := copyFSWithReplace(local.FS, DefaultContractPath, raw)
	if err != nil {
		return res, fmt.Errorf("carrying acceptances into pacto.yaml: %w", err)
	}
	c := *local.Contract
	c.Dependencies = deps
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
// into raw and deps, cites every record that carried something in res and
// returns the pinned fields.
func pinAccepted(js []delivery.JudgedAcceptance, raw *[]byte, deps []contract.Dependency, res *carryResult) map[string]bool {
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
			if latest[r.Field] != i {
				continue
			}
			if why := pinField(raw, deps, r); why != "" {
				res.Notes = append(res.Notes, why+"; acceptance "+j.Ref+" not applied")
				continue
			}
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

// dependencyField splits dependencies[<name>].<key> for key ref or
// compatibility.
func dependencyField(field string) (name, key string, ok bool) {
	rest, found := strings.CutPrefix(field, "dependencies[")
	name, key, _ = strings.Cut(rest, "].")
	return name, key, found && name != "" && (key == "ref" || key == "compatibility")
}
