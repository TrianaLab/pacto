package oci_test

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
)

// pkg/orgindex may not import pkg/oci, so the writers keep their own constants;
// this pins that they agree with the push scope's names.
func TestInstallationNamesMatchWriters(t *testing.T) {
	if oci.TargetsRepository != delivery.TargetSnapshotRepository {
		t.Errorf("TargetsRepository = %q, delivery writes %q", oci.TargetsRepository, delivery.TargetSnapshotRepository)
	}
	if oci.IndexRepository != orgindex.RepositoryName {
		t.Errorf("IndexRepository = %q, orgindex writes %q", oci.IndexRepository, orgindex.RepositoryName)
	}
}
