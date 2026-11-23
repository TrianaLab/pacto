package managedres

import (
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

func TestParseLocation(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		loc      Location
	}{
		{in: "s3://acme-tf/prod/app.tfstate?region=eu-west-1", want: "s3://acme-tf/prod/app.tfstate?region=eu-west-1",
			loc: Location{Scheme: SchemeS3, Host: "acme-tf", Key: "prod/app.tfstate", Region: "eu-west-1"}},
		{in: "s3://acme-tf/app.tfstate", want: "s3://acme-tf/app.tfstate", loc: Location{Scheme: SchemeS3, Host: "acme-tf", Key: "app.tfstate"}},
		{in: "gs://acme-tf/env/prod/default.tfstate", want: "gs://acme-tf/env/prod/default.tfstate",
			loc: Location{Scheme: SchemeGCS, Host: "acme-tf", Key: "env/prod/default.tfstate"}},
		{in: "azurerm://acmetf/tfstate/prod.terraform.tfstate", want: "azurerm://acmetf/tfstate/prod.terraform.tfstate",
			loc: Location{Scheme: SchemeAzure, Host: "acmetf", Container: "tfstate", Key: "prod.terraform.tfstate"}},
		// Refused: credentials, escapes, fragments, other query keys, a walk out.
		{in: "s3://AKIA:secret@acme-tf/app.tfstate"},
		{in: "s3://acme-tf/app.tfstate?region=eu-west-1&token=x"},
		{in: "s3://acme-tf/app.tfstate?region=eu-west-1&region=us-east-1"},
		{in: "s3://acme-tf/app.tfstate?region=EU_WEST"},
		{in: "gs://acme-tf/app.tfstate?region=eu"},
		{in: "s3://acme-tf/app.tfstate#x"},
		{in: "s3://acme-tf/%2e%2e/app.tfstate"},
		{in: "s3://acme-tf/../app.tfstate"},
		{in: "s3://acme-tf/./app.tfstate"},
		{in: "s3://acme-tf//app.tfstate"},
		{in: "s3://acme-tf/"},
		{in: "s3://acme-tf/a b"},
		{in: "s3://acme-tf/a\\b"},
		{in: "s3:acme-tf/app.tfstate"},
		{in: "http://acme-tf/app.tfstate"},
		{in: "s3://Acme_TF/app.tfstate"},
		{in: "s3://acme-tf:9000/app.tfstate"},
		{in: "azurerm://acmetf/prod.tfstate"},
		{in: "azurerm://acmetf/Tf_State/prod.tfstate"},
		{in: "::"},
	} {
		got, err := ParseLocation(tc.in)
		if tc.want == "" {
			if err == nil {
				t.Errorf("ParseLocation(%q) = %+v, want an error", tc.in, got)
			}
			continue
		}
		if err != nil || got != tc.loc || got.String() != tc.want {
			t.Errorf("ParseLocation(%q) = %+v (%s), %v", tc.in, got, got.String(), err)
		}
	}
}

func TestInventoried(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	cat := externalcatalog.Default()
	r, ok := Inventoried(cat, "aws-resource-explorer", "rds:db", "123456789012", "EU-West-1", "Orders-DB", now)
	if !ok || r.Identity != "aws/rds/123456789012/eu-west-1/orders-db" || r.Name != "Orders-DB" || r.Type != "rds:db" ||
		r.Entry != "aws/rds" || r.Catalog == "" || !r.ObservedAt.Equal(now) || r.Missing != nil {
		t.Fatalf("Inventoried = %+v %v", r, ok)
	}
	if r, _ := Inventoried(cat, "azure-resource-graph", "microsoft.sql/servers/databases", "sub", "", "orders", now); r.Identity != "azure/sql" || r.Missing[0] != "region" {
		t.Errorf("no region = %+v", r)
	}
	if _, ok := Inventoried(cat, "aws-resource-explorer", "ec2:instance", "1", "r", "i", now); ok {
		t.Error("an unclassified type was kept")
	}
}
