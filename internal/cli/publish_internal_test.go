package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

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

// wantRun checks one invocation: its error and what stdout says.
func wantRun(t *testing.T, out string, err error, wantErr bool, want ...string) {
	t.Helper()
	if (err != nil) != wantErr {
		t.Fatalf("err = %v, want error %v\n%s", err, wantErr, out)
	}
	for _, w := range want {
		if !strings.Contains(out, w) && (err == nil || !strings.Contains(err.Error(), w)) {
			t.Fatalf("output lacks %q:\n%s %v", w, out, err)
		}
	}
}

func TestPublishCommandPlan(t *testing.T) {
	var got app.PublishOptions
	out, err := runPublish(t, publishDeps(&publish.Result{Plan: publishPlan(true)}, nil, &got), "text", "--plan", "--revision", "r", "-p", "svc")
	wantRun(t, out, err, false, "1 to publish; nothing was changed")
	if !got.Plan || got.Revision != "r" || got.Path != "svc" {
		t.Fatalf("options %+v", got)
	}
	out, err = runPublish(t, publishDeps(&publish.Result{Plan: publishPlan(false)}, nil, &got), "text", "--plan")
	wantRun(t, out, err, true, "FAIL version: needs a major bump", errPlanNotReady.Error())
	// The directory is positional, as for init and validate; -p still works.
	_, err = runPublish(t, publishDeps(&publish.Result{Plan: publishPlan(true)}, nil, &got), "text", "--plan", "svc")
	if err != nil || got.Path != "svc" {
		t.Fatalf("positional dir: %v %+v", err, got)
	}
	_, err = runPublish(t, publishDeps(&publish.Result{Plan: publishPlan(true)}, nil, &got), "text", "--plan", "svc", "-p", "svc")
	wantRun(t, "", err, false)
	_, err = runPublish(t, publishDeps(&publish.Result{Plan: publishPlan(true)}, nil, &got), "text", "--plan", "a", "-p", "b")
	wantRun(t, "", err, true, "given twice")
}

func TestPublishCommandPublish(t *testing.T) {
	var got app.PublishOptions
	done := &publish.Result{Plan: publishPlan(true), Actions: []string{"pushed manifests reg/deploy@" + cliB}, Digests: map[string]string{},
		Deploy: "reg/deploy@" + cliB}
	out, err := runPublish(t, publishDeps(done, nil, &got), "text", "--propose", "prod/payments-prod")
	wantRun(t, out, err, false, "pushed manifests", "Deploy: reg/deploy@"+cliB, "Proposed "+cliB+" on prod/payments-prod")
	nothing := &publish.Result{Plan: publishPlan(true), Actions: []string{}, Digests: map[string]string{"manifests": cliB}}
	out, err = runPublish(t, publishDeps(nothing, nil, &got), "json")
	wantRun(t, out, err, false, `"actions": []`)
	out, err = runPublish(t, publishDeps(nothing, nil, &got), "text")
	wantRun(t, out, err, false, "Nothing to publish")
	out, err = runPublish(t, publishDeps(done, nil, &got), "bogus")
	wantRun(t, out, err, false)
}

func TestPublishCommandFailures(t *testing.T) {
	var got app.PublishOptions
	done := &publish.Result{Plan: publishPlan(true), Digests: map[string]string{}, Deploy: "reg/deploy@" + cliB}
	out, err := runPublish(t, publishDeps(nil, errors.New("contract validation failed"), &got), "text")
	wantRun(t, out, err, true, "contract validation failed")
	out, err = runPublish(t, publishDeps(nil, nil, &got), "text", "--propose", "payments")
	wantRun(t, out, err, true, "--propose takes <namespace>/<stage>")
	refused := publishDeps(done, nil, &got)
	refused.newDynamic = func() (dynamic.Interface, error) { return nil, errors.New("no cluster") }
	out, err = runPublish(t, refused, "text", "--propose", "prod/payments-prod")
	wantRun(t, out, err, true, "no cluster")
	out, err = runPublish(t, publishDeps(done, nil, &got), "text", "--propose", "prod/missing")
	wantRun(t, out, err, true, "published, but not proposed on prod/missing")
	noCreate := publishDeps(done, nil, &got)
	inner := noCreate.newDynamic
	noCreate.newDynamic = func() (dynamic.Interface, error) {
		c, _ := inner()
		c.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "operations", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("forbidden")
		})
		return c, nil
	}
	out, err = runPublish(t, noCreate, "text", "--propose", "prod/payments-prod")
	wantRun(t, out, err, true, "published, but proposing it on prod/payments-prod failed")
	// The propose waits for the Stage to record the version as published.
	publishPoll = time.Millisecond
	unseen := &publish.Result{Plan: publishPlan(true), Digests: map[string]string{}, Deploy: "reg/deploy@" + cliA}
	out, err = runPublish(t, publishDeps(unseen, nil, &got), "text", "--propose", "prod/payments-prod", "--propose-timeout", "5ms")
	wantRun(t, out, err, true, "did not record "+cliA+" as published within 5ms")
	// A Stage that takes its candidates from another Stage has nothing to wait
	// for: the version is proposed at once.
	fromUpstream := publishDeps(unseen, nil, &got)
	cluster := fromUpstream.newDynamic
	fromUpstream.newDynamic = func() (dynamic.Interface, error) {
		c, err := cluster()
		c.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "stages", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, u("delivery.pacto.trianalab.io/v1alpha1", "Stage", "payments-prod", map[string]any{
				"spec": map[string]any{"service": map[string]any{"name": "payments"}, "applyRef": map[string]any{"name": "payments"},
					"candidates": map[string]any{"upstreamStage": map[string]any{"name": "payments-test"}}}}), nil
		})
		return c, err
	}
	out, err = runPublish(t, fromUpstream, "text", "--propose", "prod/payments-prod", "--propose-timeout", "5ms")
	wantRun(t, out, err, false, "Proposed "+cliA+" on prod/payments-prod")
	// A refused publish never claims everything is published.
	refusedRes := &publish.Result{Plan: publishPlan(false), Actions: []string{}, Digests: map[string]string{}, Refusal: "the refusal is kept, signed, in reg/pacto/payments"}
	out, err = runPublish(t, publishDeps(refusedRes, publish.ErrNotReady, &got), "text")
	wantRun(t, out, err, true, "FAIL version")
	wantRun(t, out, err, true, "Refusal: the refusal is kept, signed, in reg/pacto/payments")
	if strings.Contains(out, "Nothing to publish") {
		t.Fatalf("a refused publish claims nothing is left to publish:\n%s", out)
	}
	// A run that failed after pushing the deploy artifact never tagged it, so
	// it names no Deploy: a script reading that line would deploy nothing.
	failedRes := &publish.Result{Plan: publishPlan(true), Actions: []string{"pushed manifests reg/deploy@" + cliB}, Digests: map[string]string{},
		Deploy: "reg/deploy@" + cliB}
	out, err = runPublish(t, publishDeps(failedRes, errors.New("signing: open cosign.key: no such file"), &got), "text")
	wantRun(t, out, err, true, "no such file")
	if strings.Contains(out, "Deploy:") {
		t.Fatalf("a failed publish names a deploy artifact:\n%s", out)
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
