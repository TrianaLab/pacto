package architecture

import (
	"regexp"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/detect"
)

var fluxVersionRe = regexp.MustCompile(`Flux (\d+\.\d+\.\d+)`)

// TestFluxInstallHintDocsMatch keeps the proposal a person copies the same
// everywhere: the docs and the install NOTES print detect.FluxInstallHint
// verbatim, it upgrades the chart the release publishes, and every page that
// names the packaged Flux names the version files/flux/VERSION pins.
func TestFluxInstallHintDocsMatch(t *testing.T) {
	for _, page := range [][]string{
		{"integrations", "kubernetes", "docs", "delivery-setup.md"},
		{"docs", "operational-graph-providers.md"},
	} {
		if !strings.Contains(readRepoFile(t, page...), "```text\n"+detect.FluxInstallHint+"\n```") {
			t.Errorf("%s has no text block that is detect.FluxInstallHint verbatim", strings.Join(page, "/"))
		}
	}
	notes := readRepoFile(t, "integrations", "kubernetes", "charts", "pacto-operator", "templates", "NOTES.txt")
	if !strings.Contains(notes, "\n"+detect.FluxInstallHint+"\n") {
		t.Error("templates/NOTES.txt does not print detect.FluxInstallHint on its own line")
	}

	ref := regexp.MustCompile(`oci://\S+`).FindString(detect.FluxInstallHint)
	repo, ok := strings.CutSuffix(ref, "/pacto-operator")
	release := readRepoFile(t, ".github", "workflows", "release.yml")
	if !ok || !strings.Contains(release, `helm push "$CHART-${VERSION}.tgz" `+repo+" ") {
		t.Errorf("detect.FluxInstallHint upgrades %q, which is not the chart release.yml pushes", ref)
	}

	version := ""
	for _, line := range strings.Split(readRepoFile(t, "integrations", "kubernetes", "charts", "pacto-operator", "files", "flux", "VERSION"), "\n") {
		if v, ok := strings.CutPrefix(line, "version: "); ok {
			version = v
		}
	}
	if version == "" {
		t.Fatal("files/flux/VERSION names no version")
	}
	for _, page := range [][]string{
		{"integrations", "kubernetes", "docs", "delivery-setup.md"},
		{"integrations", "kubernetes", "docs", "profiles.md"},
		{"integrations", "kubernetes", "charts", "pacto-operator", "README.md"},
	} {
		named := fluxVersionRe.FindAllStringSubmatch(readRepoFile(t, page...), -1)
		if len(named) == 0 {
			t.Errorf("%s does not name Flux %s", strings.Join(page, "/"), version)
		}
		for _, m := range named {
			if m[1] != version {
				t.Errorf("%s names Flux %s, files/flux/VERSION pins %s", strings.Join(page, "/"), m[1], version)
			}
		}
	}
}
