package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// TestDeliveryViewCommandsMatchRoutes: each view command prints the
// dashboard route's answer, with each flag passed as the route's parameter.
func TestDeliveryViewCommandsMatchRoutes(t *testing.T) {
	d := readDeps(nil)
	id := baseDashboardIdentity(&rest.Config{Host: "https://k"}, d, nil, delivery.PolicyNamespace)
	api := newDashDelivery(id, actFor(t, d), "")
	h := dashboard.NewServer(dashboard.Options{Delivery: func(*http.Request) (dashboard.DeliveryAPI, error) { return api, nil }}).APIHandler()
	for _, run := range []struct {
		args  []string
		route string
	}{
		{[]string{"inbox"}, "inbox"},
		{[]string{"inbox", "--held", "1", "--except", "prod", "--except", "", "--q", "pay"}, "inbox?held=1&except=prod&except=&q=pay"},
		{[]string{"environments", "--environment", "staging"}, "environments?environment=staging"},
		{[]string{"autonomy"}, "autonomy"},
		{[]string{"published"}, "published"},
		{[]string{"refusals"}, "refusals"},
		{[]string{"caller"}, "caller"},
	} {
		out, err := runDelivery(t, d, "text", run.args...)
		expect(t, err == nil, "%v: %v", run.args, err)
		sameAnswer(t, run.route, out, routeBody(t, h, context.Background(), "/api/delivery/"+run.route), "asOf")
	}
	// -n scopes the view as the dashboard's --namespace does.
	out, _ := runDelivery(t, d, "json", "environments", "-n", "staging")
	expect(t, listingOf(t, out).Completeness == "complete" && strings.Contains(out, "orders-staging") && !strings.Contains(out, "payments-prod"),
		"-n staging: %s", out)
}

func TestDeliveryViewCommandsFail(t *testing.T) {
	_, err := runDelivery(t, deliveryDeps("alice", errors.New("no kubeconfig")), "text", "inbox")
	wantErr(t, err, "no kubeconfig")
	d := readDeps(nil)
	d.served = func(*rest.Config, string) (bool, error) { return false, nil }
	_, err = runDelivery(t, d, "text", "published")
	wantErr(t, err, "DeliveryNotInstalled")
}

// TestDeliveryViewHelpSaysWhatIsLeftOut: a namespace the caller's access
// review filters out is left out without being named, so the help never
// promises unreadable names every namespace the caller cannot read.
func TestDeliveryViewHelpSaysWhatIsLeftOut(t *testing.T) {
	long := newDeliveryViewCommand(readDeps(nil), "environments", "").Long
	expect(t, strings.Contains(long, "leaves out, without naming them, those outside your permissions") &&
		!strings.Contains(long, "every namespace you can list"), "help: %s", long)
}
