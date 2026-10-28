package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

func TestInit_ReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	readOnlyDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readOnlyDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyDir, 0755) })

	orig, _ := os.Getwd()
	if err := os.Chdir(readOnlyDir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	svc := NewService(nil, nil)
	_, err := svc.Init(context.Background(), InitOptions{Name: "new-svc"})
	if err == nil {
		t.Error("expected error when creating dirs in read-only directory")
	}
}

func TestInit_Success(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	svc := NewService(nil, nil)
	result, err := svc.Init(context.Background(), InitOptions{Name: "my-svc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Dir != "my-svc" {
		t.Errorf("expected Dir=my-svc, got %s", result.Dir)
	}
	if result.Path != filepath.Join("my-svc", "pacto.yaml") {
		t.Errorf("expected Path=%s, got %s", filepath.Join("my-svc", "pacto.yaml"), result.Path)
	}

	// Verify directory structure
	for _, sub := range []string{"", "interfaces", "configuration"} {
		p := filepath.Join(dir, "my-svc", sub)
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", p)
		}
	}

	// Verify pacto.yaml exists
	if _, err := os.Stat(filepath.Join(dir, "my-svc", "pacto.yaml")); err != nil {
		t.Fatalf("expected pacto.yaml: %v", err)
	}

	// Verify openapi.yaml exists
	if _, err := os.Stat(filepath.Join(dir, "my-svc", "interfaces", "openapi.yaml")); err != nil {
		t.Fatalf("expected openapi.yaml: %v", err)
	}

	// Verify schema.json exists
	if _, err := os.Stat(filepath.Join(dir, "my-svc", "configuration", "schema.json")); err != nil {
		t.Fatalf("expected schema.json: %v", err)
	}

}

// TestInit_NameFromFolder: with no name and no folder, init writes here and
// names the service after the folder; a folder name with nothing usable in it
// is refused with the flag to pass.
func TestInit_NameFromFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Orders API")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	res, err := NewService(nil, nil).Init(context.Background(), InitOptions{})
	if err != nil || res.Service != "orders-api" || res.Dir != "." {
		t.Fatalf("init: %+v %v", res, err)
	}

	bad := filepath.Join(t.TempDir(), "___")
	if err := os.Mkdir(bad, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(nil, nil).Init(context.Background(), InitOptions{Dir: bad}); err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("want a name error, got %v", err)
	}
}

// TestInit_DirectoryExists: an existing empty folder gets the blank contract,
// and running init again adds nothing and keeps every file.
func TestInit_DirectoryExists(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("existing", 0755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, nil)
	if _, err := svc.Init(context.Background(), InitOptions{Name: "existing"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("existing", "pacto.yaml"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Init(context.Background(), InitOptions{Name: "existing"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files {
		if f.Action != "keep" {
			t.Errorf("second run would touch %s (%s)", f.Path, f.Action)
		}
	}
	if data, _ := os.ReadFile(filepath.Join("existing", "pacto.yaml")); string(data) != "mine\n" {
		t.Fatalf("the user's pacto.yaml was rewritten: %q", data)
	}
}

func TestInit_WriteFileErrors(t *testing.T) {
	for _, failFile := range []string{"pacto.yaml", "openapi.yaml", "schema.json"} {
		t.Run(failFile, func(t *testing.T) {
			orig, _ := os.Getwd()
			dir := t.TempDir()
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Chdir(orig) }()

			writeFileHook := func(name string, data []byte, perm os.FileMode) error {
				if strings.Contains(name, failFile) {
					return fmt.Errorf("write failed")
				}
				return os.WriteFile(name, data, perm)
			}

			svc := NewService(nil, nil)
			svc.sys.writeFile = writeFileHook
			_, err := svc.Init(context.Background(), InitOptions{Name: "test-svc"})
			if err == nil {
				t.Errorf("expected error when writing %s fails", failFile)
			}
		})
	}
}

func assertScaffoldOwner(t *testing.T, o contract.Owner) {
	t.Helper()
	if o.IsEmpty() {
		t.Fatal("expected object owner to be present")
	}
	if o.Team == "" {
		t.Error("expected owner.team to be non-empty")
	}
	if o.DRI == "" {
		t.Error("expected owner.dri to be non-empty")
	}
	if len(o.Contacts) == 0 {
		t.Error("expected owner.contacts to be non-empty")
	}
}

func assertScaffoldReadiness(t *testing.T, r *contract.Readiness) {
	t.Helper()
	if r == nil {
		t.Fatal("expected readiness to be present")
	}
	minScoreOK := r.MinScore != nil && *r.MinScore > 0
	expiresOK := r.Expires != ""
	historyOK := len(r.History) > 0
	checksOK := len(r.Claims) > 0
	if !minScoreOK || !expiresOK || !historyOK || !checksOK {
		t.Errorf("readiness incomplete: minScore>0=%v expires=%v history=%v claims=%v", minScoreOK, expiresOK, historyOK, checksOK)
	}
}

func TestInit_ScaffoldValidates(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	svc := NewService(nil, nil)
	result, err := svc.Init(context.Background(), InitOptions{Name: "my-svc"})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	content, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("failed to read scaffolded pacto.yaml: %v", err)
	}

	c, err := contract.Parse(strings.NewReader(string(content)))
	if err != nil {
		t.Fatalf("failed to parse scaffolded contract: %v", err)
	}

	if c.PactoVersion != "2.0" {
		t.Errorf("expected pactoVersion 2.0, got %s", c.PactoVersion)
	}

	assertScaffoldOwner(t, c.Service.Owner)
	assertScaffoldReadiness(t, c.Readiness)

	if validationResult := validation.ValidateStructuralRaw(content); !validationResult.IsValid() {
		t.Errorf("scaffolded contract failed validation: %v", validationResult.Errors)
	}
}

// TestInit_PactifiedContractPublishes: the contract init reads out of a plain
// service (the story's fraud: a Dockerfile and two manifests) is one pacto
// publish accepts: its plan is ready and lists the bundle and both artifacts,
// all in the registry read from the Deployment's image.
func TestInit_PactifiedContractPublishes(t *testing.T) {
	host, reg := publishRegistry(t)
	dir := filepath.Join(t.TempDir(), "fraud")
	files := map[string]string{
		"Dockerfile":          "FROM python:3.13-alpine\nCOPY app.py /app/app.py\nEXPOSE 8080\n",
		"k8s/deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: fraud}\nspec: {template: {spec: {containers: [{name: fraud, image: '" + host + "/story/images/fraud:1.0.0'}]}}}\n",
		"k8s/service.yaml":    "apiVersion: v1\nkind: Service\nmetadata: {name: fraud}\n",
	}
	for p, c := range files {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeGit(t, dir, map[string]string{"HEAD": commitA + "\n"})
	f := &fakeRun{}
	svc := publishService(f, reg)
	res, err := svc.Init(context.Background(), InitOptions{Dir: dir, Owner: "risk", Delivery: true})
	if err != nil || !res.Applied {
		t.Fatalf("init: %+v %v", res, err)
	}
	if !slices.Contains(res.Found, "registry "+host+"/story, from the image "+host+"/story/images/fraud:1.0.0") {
		t.Fatalf("found: %v", res.Found)
	}
	keyFiles(t, dir) // init's .pactoignore keeps it out of the bundle
	pub, err := svc.Publish(context.Background(), PublishOptions{Path: dir, Plan: true})
	if err != nil {
		t.Fatal(err)
	}
	if !pub.Plan.Ready || pub.Plan.Missing != 3 || !slices.Equal(f.calls, preflights) {
		t.Fatalf("publish plan: %+v", pub.Plan)
	}
}
