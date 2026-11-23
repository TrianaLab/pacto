package externalcatalog

import (
	"strings"
	"testing"
)

func TestEntryInventoryValidation(t *testing.T) {
	entry := func(class, row string) string {
		id := "aws/rds"
		if class == ClassVendorAPI {
			id = "stripe"
		}
		return head + "  - identity: " + id + "\n    class: " + class + "\n    inventory: [\"" + row + "\"]\n"
	}
	for name, row := range map[string]string{
		"aws":                 "rds:db",
		"gcp":                 "pubsub.googleapis.com/Topic",
		"gcp kind keeps case": "sqladmin.googleapis.com/Instance",
		"azure":               "microsoft.sql/servers/databases",
	} {
		if err := mustParse(t, entry(ClassManagedCloud, row)).Validate(); err != nil {
			t.Errorf("%s: %q refused: %v", name, row, err)
		}
	}
	for name, row := range map[string]string{
		"azure upper case": "Microsoft.Sql/servers",
		"aws upper case":   "RDS:db",
		"space":            "rds: db",
		"quote":            `rds:db\"`,
		"too long":         "a" + strings.Repeat("b", 200),
	} {
		t.Run(name, func(t *testing.T) {
			wantContains(t, validateErr(t, entry(ClassManagedCloud, row)), "entries[0].inventory")
		})
	}
	wantContains(t, validateErr(t, entry(ClassVendorAPI, "rds:db")), "entries[0]", "managed-cloud")
}

func TestInventoryRowClaimedOnce(t *testing.T) {
	got := validateErr(t, head+
		"  - identity: aws/rds\n    class: managed-cloud\n    inventory: [\"rds:db\"]\n"+
		"  - identity: aws/x\n    class: managed-cloud\n    inventory: [\"RDS:db\"]\n")
	wantContains(t, got, "entries[1].inventory", "already claimed by entries[0]")
}

func TestMergeOrganizationInventoryReplacesDefault(t *testing.T) {
	def := mustParse(t, head+"  - identity: aws/rds\n    class: managed-cloud\n    inventory: [\"rds:db\", \"rds:cluster\"]\n")
	org := mustParse(t, head+"  - identity: corp/db\n    class: managed-cloud\n    inventory: [\"rds:db\"]\n")
	c := Merge(def, DefaultRef(), org, &Ref{Origin: OriginOrganization, Digest: "sha256:org"})
	if e, _, _ := c.Entry("aws/rds"); strings.Join(e.Inventory, ",") != "rds:cluster" {
		t.Fatalf("the default entry keeps an inventory row the organization claims: %+v", e.Inventory)
	}
	if o, _, _ := c.Entry("corp/db"); strings.Join(o.Inventory, ",") != "rds:db" {
		t.Fatalf("organization entry = %+v", o.Inventory)
	}
}

// Every managed-cloud entry of the default catalog that names a Crossplane
// kind also names a kind of its own cloud's operator: ACK on AWS, Config
// Connector on GCP and ASO on Azure.
func TestDefaultCatalogNamesCloudOperatorKinds(t *testing.T) {
	operator := map[string]string{"aws": ".services.k8s.aws/", "gcp": ".cnrm.cloud.google.com/", "azure": ".azure.com/"}
	f := mustParse(t, string(DefaultBytes()))
	for _, e := range f.Entries {
		if e.Class != ClassManagedCloud || len(e.Crossplane) == 0 {
			continue
		}
		suffix := operator[e.Identity.Segments()[0]]
		found := false
		for _, k := range e.Crossplane {
			found = found || (suffix != "" && strings.Contains(k, suffix))
		}
		if !found {
			t.Errorf("%s names no cloud operator kind: %v", e.Identity, e.Crossplane)
		}
		if len(e.Inventory) == 0 {
			t.Errorf("%s names no cloud inventory type", e.Identity)
		}
	}
}
