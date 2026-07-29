package app

import (
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/otelobserver"
)

func TestService_ObserveOTel(t *testing.T) {
	trace := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"web"}}]},
	  "scopeSpans":[{"spans":[{"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"payments"}}]}]}]}]}`
	svc := NewService(nil, nil)
	edges, sets, err := svc.ObserveOTel([]byte(trace), otelobserver.Options{
		ObservedAt:  time.Unix(1, 0).UTC(),
		ContractRef: func(string) string { return "oci://x@sha256:a" },
	})
	if err != nil {
		t.Fatalf("ObserveOTel: %v", err)
	}
	if len(edges) != 1 || edges[0].From != "web" || edges[0].To != "payments" {
		t.Errorf("edges = %+v", edges)
	}
	if len(sets) != 1 || sets[0].Subject.Name != "web" {
		t.Errorf("sets = %+v", sets)
	}
}

func TestService_ObserveOTel_ParseError(t *testing.T) {
	svc := NewService(nil, nil)
	if _, _, err := svc.ObserveOTel([]byte("{bad"), otelobserver.Options{}); err == nil {
		t.Fatal("expected parse error")
	}
}
