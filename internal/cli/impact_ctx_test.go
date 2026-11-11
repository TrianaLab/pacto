package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// TestMCPRegressionUsesTheCallContext: a stdio server reads delivery for a
// regressed event under the call's context, so a cancelled call stops the
// namespace walk instead of running on under the server's lifetime context.
func TestMCPRegressionUsesTheCallContext(t *testing.T) {
	d := defaultDeps()
	d.newDynamic = func() (dynamic.Interface, error) {
		gvrStages := schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "stages"}
		c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
			gvrStages:                               "StageList",
			{Version: "v1", Resource: "namespaces"}: "NamespaceList",
		}, u("v1", "Namespace", "prod", nil))
		c.PrependReactor("list", "stages", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetNamespace() == "" {
				return true, nil, apierrors.NewForbidden(gvrStages.GroupResource(), "", errors.New("no"))
			}
			return false, nil, nil
		})
		return c, nil
	}
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", d)
	cmd.SetContext(context.Background()) // the server's lifetime context stays live
	call, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := mcpImpactProvider(cmd, svc, d, true, true)(call, &fleet.FleetSnapshot{},
		pactomcp.ImpactRequest{Event: &impact.Event{Kind: impact.EventRegressed, Service: "orders", Revision: "sha256:a"}})
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, l := range res.Limitations {
		msgs = append(msgs, l.Message)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), context.Canceled.Error()) {
		t.Fatalf("the walk must stop with the call: %v", msgs)
	}
}
