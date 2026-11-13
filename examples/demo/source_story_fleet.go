package main

import (
	"io/fs"
	"slices"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// storyRevisions are the payments story's contracts as fleet revisions in the
// story's own domain, so the demo's Stages join the fleet: the contract a Stage
// runs (story.Revision) is a revision here with the same digest, and the
// dashboard links the Stage row to its revision page. The bundles are indexed by
// resolved ref for the impact provider, as the embedded ones are.
func storyRevisions() ([]fleet.RawRevision, map[string]*contract.Bundle, error) {
	services, err := fs.ReadDir(story.Files, "contracts")
	if err != nil {
		return nil, nil, err
	}
	var out []fleet.RawRevision
	byRef := map[string]*contract.Bundle{}
	for _, s := range services {
		versions, err := fs.ReadDir(story.Files, "contracts/"+s.Name())
		if err != nil {
			return nil, nil, err
		}
		for _, v := range versions {
			r, err := story.Revision(s.Name(), v.Name())
			if err != nil {
				return nil, nil, err
			}
			out = append(out, fleet.RawRevision{Bundle: r.Bundle, Domain: story.Domain,
				RequestedRef: "oci://" + story.Domain + "/" + s.Name() + ":" + v.Name(),
				ResolvedRef:  r.ContractRef, Digest: delivery.ContractDigest(r.ContractRef)})
			byRef[r.ContractRef] = r.Bundle
		}
	}
	return slices.Clip(out), byRef, nil
}
