package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

const guaranteeProvider = `pactoVersion: "2.2"
service: {name: orders, version: 1.0.0}
interfaces:
  - {name: events, type: asyncapi, ref: events.yaml, guarantees: {delivery: at-least-once, dedupeWindow: 1m}}
`

func guaranteeConsumer(deps string) string {
	return "pactoVersion: \"2.2\"\nservice: {name: checkout, version: 1.0.0}\ndependencies:\n" + deps
}

func TestValidate_Guarantees(t *testing.T) {
	tests := []struct {
		name      string
		deps      string
		wantValid bool
		errors    []string // code: message
		warnings  []string
		stage     string // status, then reason when there is one
	}{
		{
			name:      "met requirement passes",
			deps:      "  - {name: orders, ref: ../orders, compatibility: ^1.0.0, required: true, requires: [{interface: events, delivery: at-least-once}]}\n",
			wantValid: true,
			stage:     "pass",
		},
		{
			name: "every mismatch on a required dependency is an error",
			deps: "  - {name: orders, ref: ../orders, compatibility: ^1.0.0, required: true, requires: [{interface: events, delivery: at-most-once, dedupeWindow: 5m}]}\n",
			errors: []string{
				// in file order, then by message: both name the same requires
				"GUARANTEE_UNMET: checkout requires events dedupeWindow >= 5m from orders, which offers 1m",
				"GUARANTEE_UNMET: checkout requires events delivery at-most-once from orders, which offers at-least-once",
			},
			stage: "fail",
		},
		{
			name:      "a mismatch on an optional dependency is a warning",
			deps:      "  - {name: orders, ref: ../orders, compatibility: ^1.0.0, required: false, requires: [{interface: events, idempotent: true}]}\n",
			wantValid: true,
			warnings:  []string{"GUARANTEE_UNMET: checkout requires events idempotent true from orders, which offers nothing"},
			stage:     "pass",
		},
		{
			name:      "an unresolvable provider is unverified",
			deps:      "  - {name: ledger, ref: ../ledger, compatibility: ^1.0.0, required: true, requires: [{interface: events, delivery: at-least-once}]}\n",
			wantValid: true,
			warnings:  []string{"GUARANTEE_UNVERIFIED: cannot check what checkout requires from ledger: "},
			stage:     "skip could not verify: ledger",
		},
		{
			name:      "an external dependency without a contract is unverified",
			deps:      "  - {name: model, external: true, version: \"2025-01\", required: true, requires: [{interface: chat, timeout: 5s}]}\n",
			wantValid: true,
			warnings:  []string{"GUARANTEE_UNVERIFIED: cannot check what checkout requires from model: it has no ref"},
			stage:     "skip could not verify: model",
		},
		{
			name: "one verified and one unverified is no pass",
			deps: "  - {name: orders, ref: ../orders, compatibility: ^1.0.0, required: true, requires: [{interface: events, delivery: at-least-once}]}\n" +
				"  - {name: ledger, ref: ../ledger, compatibility: ^1.0.0, required: true, requires: [{interface: events, delivery: at-least-once}]}\n",
			wantValid: true,
			warnings:  []string{"GUARANTEE_UNVERIFIED: cannot check what checkout requires from ledger: "},
			stage:     "skip could not verify: ledger",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeClosureFile(t, root, "orders/pacto.yaml", guaranteeProvider)
			writeClosureFile(t, root, "orders/events.yaml", "asyncapi: 3.0.0\ninfo: {title: orders, version: 1.0.0}\n")
			writeClosureFile(t, root, "checkout/pacto.yaml", guaranteeConsumer(tt.deps))

			res, err := NewService(&mockBundleStore{}, nil).Validate(context.Background(), ValidateOptions{Path: filepath.Join(root, "checkout")})
			if err != nil {
				t.Fatal(err)
			}
			if res.Valid != tt.wantValid {
				t.Fatalf("valid = %v, want %v (errors %v)", res.Valid, tt.wantValid, res.Errors)
			}
			for _, st := range res.Stages {
				if got := strings.TrimSpace(st.Status + " " + st.Reason); st.Name == "guarantees" && got != tt.stage {
					t.Errorf("guarantees stage %q, want %q", got, tt.stage)
				}
			}
			var errs, warns []string
			for _, e := range res.Errors {
				errs = append(errs, e.Code+": "+e.Message)
				if e.Path != "dependencies[0].requires" {
					t.Errorf("path %q", e.Path)
				}
			}
			for _, w := range res.Warnings {
				warns = append(warns, w.Code+": "+w.Message)
			}
			if strings.Join(errs, "\n") != strings.Join(tt.errors, "\n") {
				t.Errorf("errors:\n%s\nwant:\n%s", strings.Join(errs, "\n"), strings.Join(tt.errors, "\n"))
			}
			if len(warns) != len(tt.warnings) {
				t.Fatalf("warnings %q, want %q", warns, tt.warnings)
			}
			for i := range warns {
				if !strings.HasPrefix(warns[i], tt.warnings[i]) {
					t.Errorf("warning %q, want prefix %q", warns[i], tt.warnings[i])
				}
			}
		})
	}
}
