package app

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestRenewsReadsOnlyADigest(t *testing.T) {
	d := "sha256:" + strings.Repeat("a", 64)
	for ann, want := range map[string]string{d: d, "not-a-digest": "", "": ""} {
		if got := renews(map[string]string{contract.AnnotationRenews: ann}); got != want {
			t.Errorf("renews(%q) = %q, want %q", ann, got, want)
		}
	}
}
