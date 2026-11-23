package managedres

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var testNow = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func parseFile(t *testing.T, path string, cat *externalcatalog.Catalog) []fleet.RawManagedResource {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	got, err := ParseState(f, "state-v4.json", cat, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func parseString(t *testing.T, doc string, cat *externalcatalog.Catalog) []fleet.RawManagedResource {
	t.Helper()
	got, err := ParseState(strings.NewReader(doc), "terraform.tfstate", cat, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// line is "<identity> <type> <name> <engine> [missing...]".
func line(r fleet.RawManagedResource) string {
	return strings.Join(append([]string{r.Identity, r.Type, r.Name, r.Engine}, r.Missing...), " ")
}

// stateSecrets are the attribute values of testdata/state-v4.json no reader
// may keep.
var stateSecrets = []string{"hunter2", "pg-conn-secret", "private-key-secret", "gcp-root-secret"}

func TestStateKeepsOnlyTypeIDRegion(t *testing.T) {
	got := parseFile(t, "testdata/state-v4.json", externalcatalog.Default())
	var lines []string
	for _, r := range got {
		lines = append(lines, line(r))
		if r.Source != SourceTerraform || r.Location != "state-v4.json" || r.Catalog != externalcatalog.DefaultDigest() || r.Cluster != "" ||
			!r.ObservedAt.Equal(testNow) {
			t.Errorf("resource %+v", r)
		}
	}
	want := []string{
		"aws/rds/123456789012/eu-west-1/orders-db aws_db_instance aws_db_instance.orders postgres 16.3",
		"aws/sqs/123456789012/eu-west-1/orders-events aws_sqs_queue aws_sqs_queue.events[0] ",
		`aws/sqs/123456789012/eu-west-1/orders-events-dlq aws_sqs_queue aws_sqs_queue.events["dlq"] `,
		"gcp/cloudsql/acme-prod/europe-west1/billing-db google_sql_database_instance google_sql_database_instance.billing POSTGRES_15",
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("resources\n got %q\nwant %q", lines, want)
	}
	noSecrets(t, "resources", got, append(stateSecrets, "rds.amazonaws.com", "Statement"))
	snap, err := fleet.Build(t.Context(), fleet.BuildOptions{Now: func() time.Time { return testNow }},
		fleet.NewMemorySource("tf", SourceTerraform, &fleet.Collection{ManagedResources: got}))
	if err != nil {
		t.Fatal(err)
	}
	if rec := snap.Externals["aws/rds/123456789012/eu-west-1/orders-db"]; rec == nil || len(rec.Resources) != 1 {
		t.Fatalf("snapshot misses the resource: %+v", rec)
	}
	noSecrets(t, "snapshot", snap, stateSecrets)
}

// noSecrets fails when the JSON of v holds any of secrets.
func noSecrets(t *testing.T, what string, v any, secrets []string) {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(out), secret) {
			t.Errorf("%s holds %q", what, secret)
		}
	}
}

func TestStateSkipsSensitiveIDAttribute(t *testing.T) {
	// Merge never validates, so an unvalidated catalog can name a sensitive id
	// attribute; the reader still refuses it.
	cat := externalcatalog.Merge(&externalcatalog.File{Entries: []externalcatalog.Entry{{
		Identity: "aws/rds", Class: externalcatalog.ClassManagedCloud, Terraform: []string{"aws_db_instance:password"},
	}}}, externalcatalog.DefaultRef(), nil, nil)
	got := parseString(t, `{"version":4,"resources":[{"mode":"managed","type":"aws_db_instance","name":"main","instances":[
		{"attributes":{"password":"hunter2","arn":"arn:aws:rds:eu-west-1:123456789012:db:x"}}]}]}`, cat)
	if len(got) != 1 || line(got[0]) != "aws/rds aws_db_instance aws_db_instance.main  id" {
		t.Fatalf("resources %+v", got)
	}
}

func TestStateRejectsOtherVersion(t *testing.T) {
	_, err := ParseState(strings.NewReader(`{"version":3,"modules":[]}`), "old.tfstate", externalcatalog.Default(), testNow)
	if err == nil || !strings.Contains(err.Error(), "version 3") {
		t.Fatalf("err = %v", err)
	}
	if _, err := ParseState(strings.NewReader(`{"version":`), "bad.tfstate", externalcatalog.Default(), testNow); err == nil {
		t.Fatal("a truncated document parsed")
	}
}

// failingReader fails every read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("disk gone") }

// spaces is an endless reader of JSON whitespace.
type spaces struct{}

func (spaces) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

func TestStateTooLarge(t *testing.T) {
	_, err := ParseState(io.MultiReader(strings.NewReader(`{"version":4}`), spaces{}), "big.tfstate", externalcatalog.Default(), testNow)
	if err == nil || !strings.Contains(err.Error(), "64 MiB") {
		t.Fatalf("err = %v", err)
	}
	if _, err := ParseState(failingReader{}, "x.tfstate", externalcatalog.Default(), testNow); err == nil {
		t.Fatal("a failed read parsed")
	}
}

func TestStateDataSourcesSkipped(t *testing.T) {
	got := parseString(t, `{"version":4,"resources":[
		{"mode":"data","type":"aws_db_instance","name":"d","instances":[{"attributes":{"identifier":"legacy"}}]},
		{"mode":"managed","type":"random_password","name":"p","instances":[{"attributes":{"result":"x"}}]}]}`, externalcatalog.Default())
	if len(got) != 0 {
		t.Fatalf("resources %+v", got)
	}
}

func TestManagedMissingAccountKeepsEntryIdentity(t *testing.T) {
	got := parseString(t, `{"version":4,"resources":[
		{"mode":"managed","type":"aws_db_instance","name":"a","instances":[{"attributes":{"identifier":"orders-db","region":"eu-west-1"}}]},
		{"mode":"managed","type":"aws_db_instance","name":"b","instances":[{"attributes":{"identifier":"orders-db","arn":"not-an-arn"}}]},
		{"mode":"managed","type":"aws_db_instance","name":"c","instances":[{"attributes":{"identifier":"Bad/Id","arn":"arn:aws:rds:eu-west-1:123456789012:db:x","engine":"mysql"}}]},
		{"mode":"managed","type":"azurerm_mssql_server","name":"m","instances":[{"index_key":1.5,"attributes":{"name":"ledger","location":"westeurope","version":"12.0"}}]}]}`,
		externalcatalog.Default())
	var lines []string
	for _, r := range got {
		lines = append(lines, line(r))
	}
	want := []string{
		"aws/rds aws_db_instance aws_db_instance.a  account",
		"aws/rds aws_db_instance aws_db_instance.b  account region",
		"aws/rds aws_db_instance aws_db_instance.c mysql id",
		"azure/sql azurerm_mssql_server azurerm_mssql_server.m[1.5] 12.0 account",
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("resources\n got %q\nwant %q", lines, want)
	}
}

func TestStateCatalogDigestOfOrigin(t *testing.T) {
	org := &externalcatalog.File{Entries: []externalcatalog.Entry{{
		Identity: "acme/db", Class: externalcatalog.ClassManagedCloud, Terraform: []string{"acme_db:name"},
	}}}
	cat := externalcatalog.Merge(&externalcatalog.File{}, externalcatalog.DefaultRef(), org,
		&externalcatalog.Ref{Origin: externalcatalog.OriginOrganization, Digest: "sha256:org"})
	got := parseString(t, `{"version":4,"resources":[{"mode":"managed","type":"acme_db","name":"x","instances":[{"attributes":{"name":"x"}}]}]}`, cat)
	if len(got) != 1 || got[0].Catalog != "sha256:org" || got[0].Entry != "acme/db" {
		t.Fatalf("resources %+v", got)
	}
}
