package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// publishersIn is a cluster whose namespace ns publishes registry as the
// installation's publisher registry.
func publishersIn(ns, registry string) dynamic.Interface {
	cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"namespace": ns, "name": delivery.PolicyConfigMap},
		"data":     map[string]any{delivery.PublishersKey: `{"registry":"` + registry + `"}`}}}
	return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cm)
}

// TestFleetWiresPublisherTrust: with a cluster config, fleet provenance
// reads the installation's publisher trust from the delivery namespace;
// without one it keeps the inferred rule.
func TestFleetWiresPublisherTrust(t *testing.T) {
	ok := func() (*rest.Config, error) { return &rest.Config{}, nil }
	cluster := func(dyn dynamic.Interface, err error) deps {
		return deps{clusterConfig: ok, newDynamic: func() (dynamic.Interface, error) { return dyn, err }}
	}
	cmd := &cobra.Command{}
	if publisherTrust(cmd, deps{}) != nil {
		t.Fatal("no cluster config function read a cluster")
	}
	noConfig := deps{clusterConfig: func() (*rest.Config, error) { return nil, errors.New("no kubeconfig") }}
	if publisherTrust(cmd, noConfig) != nil {
		t.Fatal("no cluster config read a cluster")
	}
	if _, _, err := publisherTrust(cmd, cluster(nil, errors.New("unreachable")))(context.Background()); err == nil {
		t.Fatal("a cluster with no client read a config")
	}
	cfg, _, err := publisherTrust(cmd, cluster(publishersIn(delivery.PolicyNamespace, "ghcr.io/acme"), nil))(context.Background())
	if err != nil || cfg.Registry != "ghcr.io/acme" {
		t.Fatalf("the chart's namespace: %+v %v", cfg, err)
	}
	cmd.Flags().String("delivery-namespace", "", "")
	_ = cmd.Flags().Set("delivery-namespace", "elsewhere")
	if cfg, _, err := publisherTrust(cmd, cluster(publishersIn("elsewhere", "ghcr.io/other"), nil))(context.Background()); err != nil || cfg.Registry != "ghcr.io/other" {
		t.Fatalf("--delivery-namespace: %+v %v", cfg, err)
	}

	old := fleetDeps
	t.Cleanup(func() { fleetDeps = old })
	fleetDeps = func() deps { return cluster(publishersIn(delivery.PolicyNamespace, "ghcr.io/acme"), nil) }
	if fleetOptions(newFleetCommand(app.NewService(nil, nil), viper.New(), deps{}, &runRecorder{})).Provenance.Trust == nil {
		t.Error("the fleet CLI reads no installation trust with a cluster config")
	}
	fleetDeps = func() deps { return noConfig }
	if fleetOptions(newFleetCommand(app.NewService(nil, nil), viper.New(), deps{}, &runRecorder{})).Provenance.Trust != nil {
		t.Error("the fleet CLI reads installation trust with no cluster config")
	}
}
