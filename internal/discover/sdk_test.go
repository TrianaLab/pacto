package discover

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// sdkUsesOf scans dir and returns its SDK externals as
// "<identity> <path>:<line> <sdk> <major or ->", its findings' messages and
// the files read.
func sdkUsesOf(t *testing.T, dir string) (uses, findings, read []string) {
	t.Helper()
	sc, err := Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	xs, fs, files := sc.sdkUses(dir, externalcatalog.Default())
	for _, x := range xs {
		major := "-"
		if x.Major != nil {
			major = strconv.Itoa(*x.Major)
		}
		if x.Kind != "sdk" || x.Confidence != externalcatalog.ConfidencePossible || x.Where != at(x.Path, x.Line) {
			t.Errorf("external %+v", x)
		}
		uses = append(uses, x.Identity+" "+at(x.Path, x.Line)+" "+x.SDK+" "+major)
	}
	for _, f := range fs {
		findings = append(findings, f.Code+" "+f.Source+" "+f.Message)
	}
	return uses, findings, files
}

func equalUses(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSDKGoModMajorFromPath(t *testing.T) {
	uses, findings, read := sdkUsesOf(t, "testdata/sdk/go")
	equalUses(t, uses, []string{
		"stripe go.mod:6 github.com/stripe/stripe-go/v76 76",
		"aws/sqs go.mod:7 github.com/aws/aws-sdk-go-v2/service/sqs 1",
	})
	if len(findings) != 0 || !slices.Equal(read, []string{"go.mod"}) {
		t.Errorf("findings %q, read %q", findings, read)
	}
}

func TestSDKPackageLockMajor(t *testing.T) {
	uses, _, read := sdkUsesOf(t, "testdata/sdk/npm")
	equalUses(t, uses, []string{
		"stripe package.json:4 stripe 14",              // lock 14.9.0
		"twilio package.json:5 twilio 5",               // lock, though the range is not exact
		"sendgrid package.json:6 @sendgrid/mail 8",     // no lock entry: the exact ~ range
		"aws/sqs package.json:9 @aws-sdk/client-sqs 3", // optionalDependencies
	})
	if !slices.Equal(read, []string{"package.json", "package-lock.json"}) {
		t.Errorf("read %q", read)
	}
	uses, _, _ = sdkUsesOf(t, "testdata/sdk/npm-v1")
	equalUses(t, uses, []string{"stripe package.json:1 stripe 12", "twilio package.json:1 twilio -"})
}

func TestSDKPackageJSONRangeOnlyWhenExact(t *testing.T) {
	for rng, want := range map[string]string{"^14.2.0": "14", "~14": "14", "14.2.0": "14", "14.0.0-beta.1": "14", ">=14 <16": "-", "14.x": "-", "*": "-", "latest": "-"} {
		dir := writeTree(t, t.TempDir(), map[string]string{"package.json": `{"dependencies": {"stripe": "` + rng + `"}}`})
		uses, _, _ := sdkUsesOf(t, dir)
		equalUses(t, uses, []string{"stripe package.json:1 stripe " + want})
	}
	dir := writeTree(t, t.TempDir(), map[string]string{"package.json": `{"dependencies": {"stripe": 14}}`})
	uses, _, _ := sdkUsesOf(t, dir)
	equalUses(t, uses, []string{"stripe package.json:1 stripe -"})
}

func TestSDKDevDependenciesIgnored(t *testing.T) {
	uses, _, _ := sdkUsesOf(t, "testdata/sdk/npm")
	if slices.ContainsFunc(uses, func(u string) bool { return strings.HasPrefix(u, "openai ") }) {
		t.Errorf("a devDependency was read: %q", uses)
	}
	dir := writeTree(t, t.TempDir(), map[string]string{"package.json": `{"dependencies": ["stripe"], "devDependencies": {"stripe": "1.0.0"}}`})
	if uses, _, _ := sdkUsesOf(t, dir); len(uses) != 0 {
		t.Errorf("uses %q", uses)
	}
}

func TestSDKYarnLockIsUnknown(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"package.json":       `{"dependencies": {"stripe": "^14.0.0"}}`,
		"yarn.lock":          "stripe@^14.0.0:\n  version \"14.9.0\"\n",
		"web/pnpm-lock.yaml": "lockfileVersion: '9.0'\n",
	})
	uses, findings, read := sdkUsesOf(t, dir)
	equalUses(t, uses, []string{"stripe package.json:1 stripe 14"})
	equalUses(t, findings, []string{
		CodeUnsupportedFile + " web/pnpm-lock.yaml web/pnpm-lock.yaml is not read: the client major comes from package.json",
		CodeUnsupportedFile + " yarn.lock yarn.lock is not read: the client major comes from package.json",
	})
	if slices.Contains(read, "yarn.lock") {
		t.Error("yarn.lock was read")
	}
}

func TestSDKRequirementsPinnedOnly(t *testing.T) {
	uses, _, read := sdkUsesOf(t, "testdata/sdk/python")
	equalUses(t, uses, []string{
		"stripe requirements.txt:5 stripe 11",
		"twilio requirements.txt:6 Twilio -",
		"openai requirements.txt:7 openai 1",
	})
	if !slices.Equal(read, []string{"requirements.txt"}) {
		t.Errorf("read %q", read)
	}
}

func TestSDKPoetryLock(t *testing.T) {
	uses, _, _ := sdkUsesOf(t, "testdata/sdk/poetry")
	equalUses(t, uses, []string{"stripe poetry.lock:3 stripe 11"})
}

func TestSDKPomPropertyResolved(t *testing.T) {
	sc, _ := Dir("testdata/sdk/maven")
	xs, findings, _ := sc.sdkUses("testdata/sdk/maven", externalcatalog.Default())
	if len(xs) != 2 || len(findings) != 0 {
		t.Fatalf("externals %+v, findings %+v", xs, findings)
	}
	if xs[0].Identity != "stripe" || xs[0].SDK != "com.stripe:stripe-java" || xs[0].Line != 16 || xs[0].Major == nil || *xs[0].Major != 26 {
		t.Errorf("stripe = %+v", xs[0])
	}
	if xs[1].Identity != "twilio" || xs[1].Major != nil { // ${twilio.version} is not a property of the file
		t.Errorf("twilio = %+v", xs[1])
	}
	dir := writeTree(t, t.TempDir(), map[string]string{"pom.xml": "<project><dependencies>"})
	_, got, _ := sdkUsesOf(t, dir)
	equalUses(t, got, []string{CodeUnreadable + " pom.xml pom.xml " + unreadableNotParsed})
}

func TestSDKGradleLockfile(t *testing.T) {
	uses, findings, read := sdkUsesOf(t, "testdata/sdk/gradle")
	equalUses(t, uses, []string{"stripe gradle.lockfile:2 com.stripe:stripe-java 26"})
	if len(findings) != 0 || !slices.Equal(read, []string{"gradle.lockfile"}) {
		t.Errorf("findings %q, read %q", findings, read)
	}
}

func TestSDKBuildGradleWithoutLockfileIsUnknown(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{"app/build.gradle.kts": "plugins { java }\n"})
	_, findings, _ := sdkUsesOf(t, dir)
	equalUses(t, findings, []string{CodeUnsupportedFile + " app/build.gradle.kts app/build.gradle.kts is not read: commit gradle.lockfile to have Pacto read it"})
}

func TestSDKUnreadableManifests(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"go.mod":                "module x\nrequire (\n",
		"web/package.json":      "{",
		"real.txt":              "stripe==1.0.0\n",
		"api/package.json":      `{"dependencies": {"stripe": "^1.0.0"}}`,
		"api/package-lock.json": "{",
	})
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "requirements.txt")); err != nil {
		t.Fatal(err)
	}
	uses, findings, read := sdkUsesOf(t, dir)
	equalUses(t, uses, []string{"stripe api/package.json:1 stripe 1"}) // an unparsable lockfile leaves the range
	equalUses(t, findings, []string{
		CodeUnreadable + " go.mod go.mod " + unreadableNotParsed,
		CodeUnreadable + " requirements.txt requirements.txt " + unreadableNotRegular,
		CodeUnreadable + " web/package.json web/package.json " + unreadableNotParsed,
	})
	if slices.Contains(read, "requirements.txt") {
		t.Errorf("read %q", read)
	}
	readSDKFile = func(string) ([]byte, error) { return nil, os.ErrPermission }
	defer func() { readSDKFile = os.ReadFile }()
	_, findings, _ = sdkUsesOf(t, "testdata/sdk/go")
	equalUses(t, findings, []string{CodeUnreadable + " go.mod go.mod " + unreadableFailed})
	if _, f, _ := (&Scan{sdkFiles: []string{"gone.mod/go.mod"}}).sdkUses(t.TempDir(), externalcatalog.Default()); len(f) != 1 || !strings.HasSuffix(f[0].Message, unreadableFailed) {
		t.Errorf("a missing file: %+v", f)
	}
}

func TestSDKAlwaysPossible(t *testing.T) {
	rec := complete(t, "testdata/sdk/go", nil)
	f := field(t, rec.Fields, "dependencies[stripe]")
	if f.Confidence != contract.ConfidencePossible || f.Source != "go.mod:6" || f.External.Client == nil ||
		f.External.Client.SDK != "github.com/stripe/stripe-go/v76" || *f.External.Client.Major != 76 {
		t.Errorf("stripe = %+v %+v", f, f.External)
	}
	if !slices.ContainsFunc(rec.Inputs.Sources, func(s contract.InferredSourceFile) bool { return s.Path == "go.mod" }) {
		t.Errorf("sources = %+v", rec.Inputs.Sources)
	}
	// A host for the same identity makes the entry likely and keeps both sources.
	dir := writeTree(t, t.TempDir(), map[string]string{
		"requirements.txt":  "stripe>=11\n",
		"chart/Chart.yaml":  "name: c\n",
		"chart/values.yaml": "STRIPE_API_BASE: https://api.stripe.com/v1\n",
	})
	f = field(t, complete(t, dir, nil).Fields, "dependencies[stripe]")
	if f.Confidence != contract.ConfidenceLikely || len(f.External.Sources) != 2 || f.External.Client == nil || f.External.Client.Major != nil {
		t.Errorf("stripe = %+v %+v", f, f.External)
	}
	if _, err := contract.ParseInferredRecord(complete(t, dir, nil).Marshal()); err != nil {
		t.Fatal(err)
	}
}

func TestSDKNeverReadsResolvedURLs(t *testing.T) {
	for _, dir := range []string{"testdata/sdk/npm", "testdata/sdk/maven", "testdata/sdk/python"} {
		data := complete(t, dir, nil).Marshal()
		for _, leak := range []string{"npm-user", "npm-token", "maven-token", "registry.example.com", "repo.example.com", "pypi.example.com", "files.example.com", "user:pw"} {
			if bytes.Contains(data, []byte(leak)) {
				t.Errorf("%s: %q leaked", dir, leak)
			}
		}
	}
}

func TestSDKTwoMajorsNamed(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"go.mod":              "module x\n\nrequire github.com/stripe/stripe-go/v75 v75.1.0\n",
		"web/package.json":    `{"dependencies": {"stripe": "14.0.0"}}`,
		"py/requirements.txt": "stripe\n",
	})
	rec := complete(t, dir, nil)
	f := field(t, rec.Fields, "dependencies[stripe]")
	if f.External.Client.SDK != "github.com/stripe/stripe-go/v75" || *f.External.Client.Major != 75 || len(f.External.Sources) != 3 {
		t.Errorf("stripe = %+v %+v", f.External, f.External.Client)
	}
	want := contract.InferredFinding{Code: CodeAmbiguous, Source: "go.mod:3", Message: "the folder names 2 client versions of stripe: 14 (web/package.json), 75 (go.mod); the record keeps the highest"}
	if !slices.Contains(rec.Findings, want) {
		t.Errorf("findings = %+v", rec.Findings)
	}
}

func TestMajorOf(t *testing.T) {
	for v, want := range map[string]int{"v76.3.0": 76, "1.38.0": 1, "v0.0.0-20240101-abc": 0, "v2.0.0+incompatible": 2, "33.0.0-jre": 33, "7": 7} {
		if got := majorOf(v); got == nil || *got != want {
			t.Errorf("majorOf(%q) = %v, want %d", v, got, want)
		}
	}
	for _, v := range []string{"", "latest", "x1", "99999999999999999999.0"} {
		if got := majorOf(v); got != nil {
			t.Errorf("majorOf(%q) = %d", v, *got)
		}
	}
}
