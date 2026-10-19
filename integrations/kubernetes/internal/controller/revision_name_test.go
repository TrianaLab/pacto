/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"strings"
	"testing"

	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
)

func TestRevisionNameFor(t *testing.T) {
	long := strings.Repeat("1.", 200) + "0"
	cases := []struct {
		name, pacto, version, want string
	}{
		// Already-valid versions keep the legacy name byte for byte.
		{"plain semver", "svc", "1.0.0", "svc-1-0-0-abc1234"},
		{"lowercase prerelease", "svc", "1.0.0-rc.1", "svc-1-0-0-rc-1-abc1234"},
		{"double dash survives", "svc", "1.0.0--beta", "svc-1-0-0--beta-abc1234"},
		{"dotted pacto name", "a.b", "2.0.0", "a.b-2-0-0-abc1234"},
		// Invalid ones are sanitised.
		{"build metadata", "svc", "1.0.0+build.1", "svc-1-0-0-build-1-abc1234"},
		{"upper case", "svc", "1.0.0-RC1", "svc-1-0-0-rc1-abc1234"},
		{"runs collapse and trim", "svc", "+_1..0_+", "svc-1-0-abc1234"},
		{"nothing left of version", "svc", "+++", "svc-abc1234"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := revisionNameFor(tc.pacto, tc.version, "abc1234")
			if got != tc.want {
				t.Errorf("revisionNameFor(%q, %q) = %q, want %q", tc.pacto, tc.version, got, tc.want)
			}
			if errs := k8svalidation.IsDNS1123Subdomain(got); len(errs) != 0 {
				t.Errorf("%q is not a valid name: %v", got, errs)
			}
		})
	}

	t.Run("long version keeps the hash and the length cap", func(t *testing.T) {
		got := revisionNameFor("svc", long, "abc1234")
		if len(got) > k8svalidation.DNS1123SubdomainMaxLength || !strings.HasSuffix(got, "-abc1234") {
			t.Fatalf("got %q (len %d)", got, len(got))
		}
		if errs := k8svalidation.IsDNS1123Subdomain(got); len(errs) != 0 {
			t.Fatalf("%q is not a valid name: %v", got, errs)
		}
	})
}

func TestVersionLabelValue(t *testing.T) {
	cases := map[string]string{
		"1.0.0":         "1.0.0",
		"1.0.0-RC1":     "1.0.0-RC1",
		"1.0.0+build.1": "1.0.0-build.1",
		"+1.0.0+":       "1.0.0",
	}
	for in, want := range cases {
		if got := versionLabelValue(in); got != want {
			t.Errorf("versionLabelValue(%q) = %q, want %q", in, got, want)
		}
	}

	a := versionLabelValue(strings.Repeat("9", 70) + "+a")
	b := versionLabelValue(strings.Repeat("9", 70) + "+b")
	for _, v := range []string{a, b} {
		if errs := k8svalidation.IsValidLabelValue(v); len(errs) != 0 {
			t.Errorf("%q is not a valid label value: %v", v, errs)
		}
	}
	if a == b {
		t.Errorf("distinct long versions collided on %q", a)
	}
}
