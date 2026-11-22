/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package linker

import (
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const (
	dA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func payments(images ...RunningImage) Workload {
	return Workload{Namespace: "shop", Kind: "Deployment", Name: "payments", Images: images}
}

func cand(service, version, repo string) Candidate {
	return Candidate{Service: service, Version: version, Bundle: service + "@" + dB, Repository: repo, Signed: true}
}

func img(repo, digest string) RunningImage { return RunningImage{Repository: repo, Digest: digest} }

func TestMatchSkipsTargetedWorkload(t *testing.T) {
	w := payments(img("ghcr.io/acme/payments", dA))
	w.Targeted, w.Annotation = true, "oci://ghcr.io/acme/contracts/payments:2.3.0"
	l, lim := Match(w, map[string][]Candidate{dA: {cand("c/payments", "2.3.0", "ghcr.io/acme/payments")}})
	if !linkEqual(l, Link{}) || lim != (fleet.Limitation{}) {
		t.Fatalf("targeted workload linked: %+v %+v", l, lim)
	}
}

func TestMatchAnnotationWinsOverDigest(t *testing.T) {
	w := payments(img("ghcr.io/acme/payments", dA))
	w.Annotation = "oci://ghcr.io/acme/contracts/payments:2.3.0"
	l, _ := Match(w, map[string][]Candidate{dA: {cand("c/other", "1.0.0", "ghcr.io/acme/payments")}})
	if l.Signal != fleet.LinkSignalAnnotation || l.Value != w.Annotation || l.Ref != w.Annotation {
		t.Fatalf("annotation did not win: %+v", l)
	}
}

func TestMatchOneServiceLinks(t *testing.T) {
	l, lim := Match(payments(img("ghcr.io/acme/payments", dA)),
		map[string][]Candidate{dA: {cand("c/payments", "2.3.0", "ghcr.io/acme/payments")}})
	want := Link{Signal: fleet.LinkSignalImageDigest, Value: dA, Ref: "c/payments@" + dB}
	if !linkEqual(l, want) || lim.Code != "" {
		t.Fatalf("got %+v %+v, want %+v", l, lim, want)
	}
}

func TestMatchNewestVersionOfOneService(t *testing.T) {
	cs := []Candidate{cand("c/payments", "2.2.0", "ghcr.io/acme/payments"), cand("c/payments", "2.3.0", "ghcr.io/acme/payments")}
	cs[1].Bundle = "c/payments@" + dA
	l, _ := Match(payments(img("ghcr.io/acme/payments", dA)), map[string][]Candidate{dA: cs})
	if l.Ref != "c/payments@"+dA {
		t.Fatalf("did not link the newest: %+v", l)
	}
}

func TestMatchSeveralVersionsMarksVersionAmbiguous(t *testing.T) {
	cs := []Candidate{
		cand("c/payments", "1.8.0", "ghcr.io/acme/payments"),
		cand("c/payments", "1.9.0", "ghcr.io/acme/payments"),
		cand("c/payments", "1.8.0", "ghcr.io/acme/payments"),
	}
	l, lim := Match(payments(img("ghcr.io/acme/payments", dA)), map[string][]Candidate{dA: cs})
	if !slices.Equal(l.OtherVersions, []string{"1.8.0"}) || lim.Code != "" || l.Signal != fleet.LinkSignalImageDigest {
		t.Fatalf("got %+v %+v", l, lim)
	}
}

func TestMatchPrereleaseIgnoredBesideRelease(t *testing.T) {
	cs := []Candidate{cand("c/payments", "2.0.0-rc.1", "ghcr.io/acme/payments"), cand("c/payments", "1.9.0", "ghcr.io/acme/payments")}
	l, _ := Match(payments(img("ghcr.io/acme/payments", dA)), map[string][]Candidate{dA: cs})
	if l.Signal == "" || len(l.OtherVersions) != 0 {
		t.Fatalf("prerelease counted beside a release: %+v", l)
	}
	only, _ := Match(payments(img("ghcr.io/acme/payments", dA)), map[string][]Candidate{dA: cs[:1]})
	if only.Signal != fleet.LinkSignalImageDigest {
		t.Fatalf("a lone prerelease did not link: %+v", only)
	}
}

func TestMatchRecordRepositoryMustMatchPodImage(t *testing.T) {
	l, lim := Match(payments(img("ghcr.io/acme/payments", dA)),
		map[string][]Candidate{dA: {cand("c/evil", "1.0.0", "ghcr.io/evil/payments")}})
	if l.Signal != "" || lim.Code != "" {
		t.Fatalf("record of another repository linked: %+v %+v", l, lim)
	}
	hub, _ := Match(payments(img("docker.io/library/nginx", dA)), map[string][]Candidate{dA: {cand("c/nginx", "1.0.0", "nginx")}})
	if hub.Signal == "" {
		t.Fatal("a Docker Hub short name did not match its spelled-out repository")
	}
}

func TestMatchUnsignedRecordIgnored(t *testing.T) {
	c := cand("c/payments", "2.3.0", "ghcr.io/acme/payments")
	c.Signed = false
	if l, _ := Match(payments(img("ghcr.io/acme/payments", dA)), map[string][]Candidate{dA: {c}}); l.Signal != "" {
		t.Fatalf("unsigned record linked: %+v", l)
	}
}

func TestMatchRefusesMalformedAnnotation(t *testing.T) {
	long := "oci://ghcr.io/" + strings.Repeat("a", 250) + ":1"
	for _, v := range []string{
		"oci://user:secret@ghcr.io/acme/payments:1.0.0",
		"ghcr.io/acme/payments:1.0.0",
		long,
		"please link me to payments",
		"oci://ghcr.io/acme/payments",
		"oci://payments:1.0.0",
		"oci://ghcr.io/acme/payments@sha256:abc",
		"oci://ghcr.io/acme/payments:1.0.0@" + dA,
	} {
		w := payments(img("ghcr.io/acme/payments", dA))
		w.Annotation = v
		l, lim := Match(w, map[string][]Candidate{dA: {cand("c/payments", "2.3.0", "ghcr.io/acme/payments")}})
		if l.Signal != "" || lim.Code != LimitationAnnotationInvalid || strings.Contains(lim.Message, v) {
			t.Errorf("%q: got %+v %+v", v, l, lim)
		}
		if !strings.Contains(lim.Message, "k8s:shop/Deployment/payments") {
			t.Errorf("%q: message does not name the workload: %s", v, lim.Message)
		}
	}
	for _, v := range []string{"oci://ghcr.io/acme/payments@" + dA, "oci://localhost:5000/payments:1.0.0"} {
		if !validAnnotation(v) {
			t.Errorf("%q refused", v)
		}
	}
}

func TestMatchTwoServicesAmbiguous(t *testing.T) {
	l, lim := Match(payments(img("ghcr.io/acme/payments", dA)), map[string][]Candidate{dA: {
		cand("c/payments", "2.3.0", "ghcr.io/acme/payments"), cand("c/billing", "1.0.0", "ghcr.io/acme/payments"),
	}})
	if l.Signal != "" || lim.Code != fleet.LimitationInferredReplacementAmbiguous ||
		!strings.Contains(lim.Message, "k8s:shop/Deployment/payments") || !strings.Contains(lim.Message, "(c/billing, c/payments)") {
		t.Fatalf("got %+v %+v", l, lim)
	}
}

func TestMatchContainersOfTwoServicesAmbiguous(t *testing.T) {
	l, lim := Match(payments(img("ghcr.io/acme/payments", dA), img("ghcr.io/acme/sidecar", dB)), map[string][]Candidate{
		dA: {cand("c/payments", "2.3.0", "ghcr.io/acme/payments")},
		dB: {cand("c/sidecar", "1.0.0", "ghcr.io/acme/sidecar")},
	})
	if l.Signal != "" || lim.Code != fleet.LimitationInferredReplacementAmbiguous {
		t.Fatalf("got %+v %+v", l, lim)
	}
}

func TestMatchNoCandidateNoLink(t *testing.T) {
	if l, lim := Match(payments(img("ghcr.io/acme/payments", dA)), nil); l.Signal != "" || lim.Code != "" {
		t.Fatalf("got %+v %+v", l, lim)
	}
}

func TestMatchNoDigestNoLink(t *testing.T) {
	l, lim := Match(payments(), map[string][]Candidate{dA: {cand("c/payments", "2.3.0", "ghcr.io/acme/payments")}})
	if l.Signal != "" || lim.Code != "" {
		t.Fatalf("got %+v %+v", l, lim)
	}
}

func linkEqual(a, b Link) bool {
	return a.Signal == b.Signal && a.Value == b.Value && a.Ref == b.Ref && slices.Equal(a.OtherVersions, b.OtherVersions)
}
