package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

func runPublish(t *testing.T, d deps, format string, args ...string) (string, error) {
	t.Helper()
	v := viper.New()
	v.Set(outputFormatKey, format)
	cmd := newPublishCommand(app.NewService(nil, nil), v, d)
	out := &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func publishPlan(ready bool) publish.Plan {
	return publish.Plan{Service: "payments", Version: "1.0.0", Revision: "abc", Ready: ready, Missing: 1,
		Checks: []publish.Check{{Name: "version", Passed: ready, Message: "needs a major bump"}},
		Items:  []publish.Item{{Name: "manifests", Type: "manifests", Ref: "reg/deploy:1.0.0", State: publish.Absent}}}
}

// publishDeps answers every publish with res and err, and records the options.
func publishDeps(res *publish.Result, err error, got *app.PublishOptions) deps {
	d := deliveryDeps("system:serviceaccount:agents:bot", nil)
	d.publish = func(_ *app.Service, _ context.Context, o app.PublishOptions) (*publish.Result, error) {
		*got = o
		return res, err
	}
	return d
}

func TestPublishCommand(t *testing.T) {
	var got app.PublishOptions
	out, err := runPublish(t, publishDeps(&publish.Result{Plan: publishPlan(true)}, nil, &got), "text", "--plan", "--revision", "r", "-p", "svc")
	if err != nil || !got.Plan || got.Revision != "r" || got.Path != "svc" || !strings.Contains(out, "1 to publish; nothing was changed") {
		t.Fatalf("plan: %v %+v\n%s", err, got, out)
	}
	out, err = runPublish(t, publishDeps(&publish.Result{Plan: publishPlan(false)}, nil, &got), "text", "--plan")
	if !errors.Is(err, errPlanNotReady) || !strings.Contains(out, "FAIL version: needs a major bump") {
		t.Fatalf("a refused plan fails: %v\n%s", err, out)
	}
	done := &publish.Result{Plan: publishPlan(true), Actions: []string{"pushed manifests reg/deploy@" + cliB}, Digests: map[string]string{},
		Deploy: "reg/deploy@" + cliB}
	out, err = runPublish(t, publishDeps(done, nil, &got), "text", "--propose", "prod/payments-prod")
	if err != nil || !strings.Contains(out, "pushed manifests") || !strings.Contains(out, "Deploy: reg/deploy@"+cliB) ||
		!strings.Contains(out, "Proposed "+cliB+" on prod/payments-prod") {
		t.Fatalf("publish and propose: %v\n%s", err, out)
	}
	nothing := &publish.Result{Plan: publishPlan(true), Actions: []string{}, Digests: map[string]string{"manifests": cliB}}
	if out, _ := runPublish(t, publishDeps(nothing, nil, &got), "json"); !strings.Contains(out, `"actions": []`) {
		t.Fatalf("json: %s", out)
	}
	if out, _ := runPublish(t, publishDeps(nothing, nil, &got), "text"); !strings.Contains(out, "Nothing to publish") {
		t.Fatalf("second run: %s", out)
	}
	if _, err := runPublish(t, publishDeps(nil, errors.New("contract validation failed"), &got), "text"); err == nil {
		t.Fatal("a validation failure surfaces")
	}
	if _, err := runPublish(t, publishDeps(nil, nil, &got), "text", "--propose", "payments"); err == nil {
		t.Fatal("--propose needs namespace/stage")
	}
	refused := publishDeps(done, nil, &got)
	refused.newDynamic = func() (dynamic.Interface, error) { return nil, errors.New("no cluster") }
	if _, err := runPublish(t, refused, "text", "--propose", "prod/payments-prod"); err == nil {
		t.Fatal("no cluster to propose on")
	}
	if _, err := runPublish(t, publishDeps(done, nil, &got), "text", "--propose", "prod/missing"); err == nil ||
		!strings.Contains(err.Error(), "published, but proposing it on prod/missing failed") {
		t.Fatalf("a missing Stage: %v", err)
	}
	if _, err := runPublish(t, publishDeps(done, nil, &got), "bogus"); err != nil {
		t.Fatal(err)
	}
}

func TestPublishCommandOutputFailure(t *testing.T) {
	var got app.PublishOptions
	v := viper.New()
	v.Set(outputFormatKey, "json")
	cmd := newPublishCommand(app.NewService(nil, nil), v, publishDeps(&publish.Result{Plan: publishPlan(true)}, nil, &got))
	cmd.SetOut(errWriter{})
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs([]string{"--plan"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("a failed write surfaces")
	}
}

func TestMCPPublishMode(t *testing.T) {
	var got app.PublishOptions
	d := publishDeps(&publish.Result{Plan: publishPlan(true)}, nil, &got)
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	_ = cmd.Flags().Set("publish", "true")
	if s, err := buildMCPServer(cmd, app.NewService(nil, nil), "v", nil, d); err != nil || s == nil {
		t.Fatalf("publish server: %v", err)
	}
	if res, err := mcpPublish(app.NewService(nil, nil), d)(context.Background(), "svc", "r", true); err != nil || !res.Plan.Ready || got.Path != "svc" || !got.Plan {
		t.Fatalf("adapter: %+v %v", got, err)
	}
	if err := checkMCPMode(false, false, false, true, true); err == nil || !strings.Contains(err.Error(), "--publish") {
		t.Fatalf("modes: %v", err)
	}
	over := newMCPCommand(app.NewService(nil, nil), "v", d)
	over.SetArgs([]string{"--publish", "-t", "http"})
	over.SetOut(&strings.Builder{})
	over.SetErr(&strings.Builder{})
	if err := over.Execute(); !errors.Is(err, errPublishOverHTTP) {
		t.Fatalf("publish over HTTP must be refused: %v", err)
	}
}
