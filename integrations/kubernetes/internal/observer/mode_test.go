/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestModeSetAcceptsBoolsAndWords(t *testing.T) {
	for in, want := range map[string]Mode{"true": ModeOn, "on": ModeOn, "false": ModeOff, "off": ModeOff, "auto": ModeAuto} {
		var m Mode
		if err := m.Set(in); err != nil || m != want {
			t.Errorf("Set(%q) = %q, %v; want %q", in, m, err, want)
		}
	}
	var m Mode
	err := m.Set("maybe")
	if err == nil {
		t.Fatal("Set(maybe) should fail")
	}
	for _, w := range []string{"on", "off", "auto"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not name %q", err, w)
		}
	}
}

func TestModeBareFlagIsOn(t *testing.T) {
	for args, want := range map[string]Mode{"--enable-probing": ModeOn, "--enable-probing=auto": ModeAuto} {
		var m Mode
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.Var(&m, "enable-probing", "")
		if err := fs.Parse([]string{args}); err != nil || m != want {
			t.Errorf("parse %q = %q, %v; want %q", args, m, err, want)
		}
	}
	var m Mode
	if !m.IsBoolFlag() {
		t.Error("Mode must be a bool flag so the bare flag means on")
	}
}

func TestModeZeroIsOff(t *testing.T) {
	var m Mode
	if m.String() != "off" {
		t.Errorf("zero Mode String() = %q, want off", m.String())
	}
	if a := ModeAuto; a.String() != "auto" {
		t.Errorf("auto String() = %q", a.String())
	}
	if s := Resolve(m, "enable-probing", "probing", nil, nil); s.On || s.Source != "--enable-probing=off" {
		t.Errorf("zero Mode resolves to %+v", s)
	}
}

func TestResolveOnOffIgnoreLabel(t *testing.T) {
	labels := map[string]string{LabelObserve: "probing"}
	if s := Resolve(ModeOn, "enable-probing", "probing", nil, errors.New("x")); !s.On || s.Source != "--enable-probing=on" {
		t.Errorf("on = %+v", s)
	}
	if s := Resolve(ModeOff, "enable-probing", "probing", labels, nil); s.On || s.Source != "--enable-probing=off" {
		t.Errorf("off = %+v", s)
	}
}

func TestResolveAutoLabelled(t *testing.T) {
	for _, v := range []string{"probing", "metrics", "probing.metrics", "metrics.probing"} {
		labels := map[string]string{LabelObserve: v}
		for _, feature := range []string{"probing", "metrics"} {
			s := Resolve(ModeAuto, "enable-"+feature, feature, labels, nil)
			want := strings.Contains(v, feature)
			if s.On != want {
				t.Errorf("label %q feature %s: On = %v, want %v", v, feature, s.On, want)
			}
			if want && s.Source != "namespace label pacto.trianalab.io/observe="+v {
				t.Errorf("label %q feature %s: source %q", v, feature, s.Source)
			}
			if !want && s.Source != fmt.Sprintf("--enable-%s=auto, namespace label pacto.trianalab.io/observe=%s does not name %s", feature, v, feature) {
				t.Errorf("label %q feature %s: source %q", v, feature, s.Source)
			}
		}
	}
}

func TestResolveAutoUnlabelledIsOff(t *testing.T) {
	s := Resolve(ModeAuto, "enable-probing", "probing", map[string]string{"other": "x"}, nil)
	if s.On || s.Source != "--enable-probing=auto, namespace has no pacto.trianalab.io/observe label" {
		t.Errorf("unlabelled = %+v", s)
	}
}

func TestResolveAutoUnreadableIsOff(t *testing.T) {
	gr := schema.GroupResource{Resource: "namespaces"}
	labels := map[string]string{LabelObserve: "probing"}
	for err, reason := range map[error]string{
		apierrors.NewForbidden(gr, "payments", errors.New("rbac says no")): "forbidden",
		apierrors.NewNotFound(gr, "payments"):                              "not found",
		apierrors.NewTimeoutError("slow", 1):                               "timeout",
		apierrors.NewServerTimeout(gr, "get", 1):                           "timeout",
		context.DeadlineExceeded:                                           "timeout",
		errors.New("connection refused to 10.0.0.1"):                       "unavailable",
	} {
		s := Resolve(ModeAuto, "enable-probing", "probing", labels, err)
		if s.On || s.Source != "--enable-probing=auto, namespace label unreadable: "+reason {
			t.Errorf("%v: %+v, want reason %q", err, s, reason)
		}
	}
}

func TestResolveUnknownTokenIgnored(t *testing.T) {
	s := Resolve(ModeAuto, "enable-probing", "probing", map[string]string{LabelObserve: "probing.everything.everything"}, nil)
	if !s.On {
		t.Fatal("known token must still apply")
	}
	if strings.Count(s.Source, "everything ignored") != 1 || !strings.HasSuffix(s.Source, "; unknown observe value everything ignored") {
		t.Errorf("source %q must name the unknown token once", s.Source)
	}
}
