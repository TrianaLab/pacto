package dashboard

import (
	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/diff"
)

// BundlePair associates a version tag with its parsed bundle for classification.
type BundlePair struct {
	Tag    string
	Bundle *contract.Bundle
}

// ClassifyVersions computes the diff classification between consecutive versions
// in a descending-sorted slice (latest first). Each version (except the oldest)
// gets a classification relative to its predecessor: "NON_BREAKING",
// "POTENTIAL_BREAKING", or "BREAKING".
//
// Classification requires materialized bundles; if either bundle in a pair is
// nil (e.g. not yet fetched from the registry), that pair is skipped and the
// version receives no classification.
//
// This is a purely derivational function — it depends only on contract bundles
// and is independent of any specific data source (cache, OCI, etc.).
func ClassifyVersions(versions []BundlePair) map[string]string {
	result := make(map[string]string, len(versions))
	for i := 0; i < len(versions)-1; i++ {
		cur := versions[i]
		prev := versions[i+1]
		if cur.Bundle != nil && prev.Bundle != nil {
			r := diff.Compare(prev.Bundle.Contract, cur.Bundle.Contract, prev.Bundle.FS, cur.Bundle.FS)
			result[cur.Tag] = r.Classification.String()
		}
	}
	return result
}
