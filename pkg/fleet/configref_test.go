package fleet

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const refSchemaJSON = `{"type":"object","properties":{"size":{"enum":["small","large"]}},"required":["size"]}`

// refBundle is a 2.6 contract of service at 1.0.0 whose configuration db is
// cfg (a flow mapping body), with files.
func refBundle(t *testing.T, service, cfg string, files fstest.MapFS) RawRevision {
	t.Helper()
	y := fmt.Sprintf("pactoVersion: \"2.6\"\nservice: {name: %s, version: 1.0.0}\nconfigurations:\n  - {name: db, required: false, %s}\n", service, cfg)
	if _, ok := files["prod.yaml"]; ok {
		y += "environments:\n  - {name: prod, file: prod.yaml}\n"
	}
	return RawRevision{Bundle: &contract.Bundle{Contract: mustParse(t, y), RawYAML: []byte(y), FS: files}, Digest: refDigest(service)}
}

// pgProvider declares configuration db with refSchemaJSON.
func pgProvider(t *testing.T) RawRevision {
	return refBundle(t, "pg", "schema: db.json", fstest.MapFS{"db.json": {Data: []byte(refSchemaJSON)}})
}

// refOrders references provider (by digest) with inline size and, when env is
// set, a prod environment giving it.
func refOrders(t *testing.T, ref, size, env string) RawRevision {
	files := fstest.MapFS{}
	if env != "" {
		files["prod.yaml"] = &fstest.MapFile{Data: []byte(env)}
	}
	return refBundle(t, "orders", fmt.Sprintf("ref: %q, values: {size: %s}", ref, size), files)
}

func refSnap(t *testing.T, revs ...RawRevision) *FleetSnapshot {
	t.Helper()
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("l", "local", &Collection{Revisions: revs}))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func revisionOf(t *testing.T, snap *FleetSnapshot, service string) *ContractRevision {
	t.Helper()
	for _, r := range snap.Revisions {
		if r.Service == service {
			return r
		}
	}
	t.Fatalf("no revision of %s", service)
	return nil
}

func stageOf(rev *ContractRevision, name string) ValidationStage {
	for _, st := range rev.Stages {
		if st.Name == name {
			return st
		}
	}
	return ValidationStage{}
}

func findingMessages(rev *ContractRevision) string {
	var out []string
	for _, f := range rev.Validation {
		out = append(out, string(f.Code)+": "+f.Message)
	}
	return strings.Join(out, "\n")
}

func TestRevalidateConfigRefs(t *testing.T) {
	pinned := "oci://r/pg@" + refDigest("pg")
	chainA := refBundle(t, "a", fmt.Sprintf("ref: %q", "oci://r/pg@"+refDigest("pg")), fstest.MapFS{})
	cycleA := refBundle(t, "a", fmt.Sprintf("ref: %q", "oci://r/b@"+refDigest("b")), fstest.MapFS{})
	cycleB := refBundle(t, "b", fmt.Sprintf("ref: %q", "oci://r/a@"+refDigest("a")), fstest.MapFS{})
	cases := []struct {
		name   string
		revs   []RawRevision
		valid  bool
		stage  string
		want   string
		envent string
	}{
		{"values pass", []RawRevision{pgProvider(t), refOrders(t, pinned, "small", "db: {size: large}\n")}, true, "pass", "", EnvironmentPass},
		{"inline values fail", []RawRevision{pgProvider(t), refOrders(t, pinned, "huge", "")}, false, "fail", "CONFIG_VALUES_VALIDATION_FAILED: configuration db at /size", ""},
		{"environment values fail", []RawRevision{pgProvider(t), refOrders(t, pinned, "small", "db: {size: tiny}\n")}, false, "pass", "ENVIRONMENT_VALUES_INVALID", EnvironmentFail},
		{"provider absent", []RawRevision{refOrders(t, pinned, "huge", "")}, true, "skip", "CONFIG_REF_UNVERIFIED: configuration db: failed to resolve ref", ""},
		{"tag only", []RawRevision{pgProvider(t), refOrders(t, "oci://r/pg:1.0.0", "huge", "")}, true, "skip", "carries no resolved identity", ""},
		{"chained", []RawRevision{pgProvider(t), chainA, refOrders(t, "oci://r/a@"+refDigest("a"), "huge", "")}, false, "fail", "CONFIG_VALUES_VALIDATION_FAILED", ""},
		{"cycle", []RawRevision{cycleA, cycleB, refOrders(t, "oci://r/a@"+refDigest("a"), "huge", "")}, false, "fail", "CONFIG_REF_CYCLE", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rev := revisionOf(t, refSnap(t, tc.revs...), "orders")
			if rev.Valid != tc.valid || stageOf(rev, "configuration-refs").Status != tc.stage || !strings.Contains(findingMessages(rev), tc.want) {
				t.Fatalf("valid=%v stages=%+v findings:\n%s", rev.Valid, rev.Stages, findingMessages(rev))
			}
			if tc.envent != "" && (len(rev.Environments) != 1 || rev.Environments[0].Status != tc.envent) {
				t.Fatalf("environments = %+v", rev.Environments)
			}
			if len(rev.localStages) != 5 || rev.localStages[3].Status != "skip" {
				t.Fatalf("local stages = %+v", rev.localStages)
			}
		})
	}
}

func TestSnapshotResolver(t *testing.T) {
	snap := refSnap(t, pgProvider(t), refOrders(t, "oci://r/pg@"+refDigest("pg"), "small", ""))
	orders := revisionOf(t, snap, "orders")
	r := snapshotResolver{snap: snap, root: orders.Key}
	if b, err := r.ResolveBundle(t.Context(), "oci://r/pg@"+refDigest("pg")); err != nil || b.Contract.Service.Name != "pg" {
		t.Fatalf("resolve = %v, %v", b, err)
	}
	if _, _, err := r.ResolveBundleFrom(t.Context(), "nope", "x"); err == nil || !strings.Contains(err.Error(), "not in this snapshot") {
		t.Errorf("unknown base: %v", err)
	}
	if _, err := r.ResolveBundle(t.Context(), "oci://r/other:1"); err == nil || !strings.Contains(err.Error(), "declares no configuration ref") {
		t.Errorf("undeclared ref: %v", err)
	}
	revisionOf(t, snap, "pg").docConflict = "sources disagree"
	if _, err := r.ResolveBundle(t.Context(), "oci://r/pg@"+refDigest("pg")); err == nil || err.Error() != "sources disagree" {
		t.Errorf("doc conflict: %v", err)
	}
}
