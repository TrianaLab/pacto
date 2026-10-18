package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/runtimewatch"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/evalsink"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/kubesink"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

type nopCollector struct{}

func (nopCollector) Run(ctx context.Context, _ chan<- runtimewatch.Event) { <-ctx.Done() }
func (nopCollector) Status() runtimewatch.CollectorStatus                 { return runtimewatch.CollectorStatus{} }
func (nopCollector) Attached() <-chan struct{}                            { return nil }
func (nopCollector) Mono() uint64                                         { return 0 }

// stubAgent replaces the seams and returns the config Run would have served.
func stubAgent(t *testing.T, collectorErr, dynErr error) (*runtimewatch.Config, deps) {
	t.Helper()
	got := &runtimewatch.Config{}
	d := defaultDeps()
	d.newCollector = func(size uint32) (runtimewatch.Collector, error) {
		if size != 4096 {
			t.Errorf("ringbuf size = %d", size)
		}
		return nopCollector{}, collectorErr
	}
	d.newDynamic = func() (dynamic.Interface, error) {
		return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), dynErr
	}
	d.runAgent = func(_ context.Context, cfg runtimewatch.Config) { *got = cfg }
	return got, d
}

func runAgentCommand(d deps, args ...string) error {
	v := viper.New()
	v.Set(outputFormatKey, "text")
	cmd := newAgentCommand(&app.Service{}, v, "v9", d)
	cmd.SetArgs(append([]string{"--ringbuf-size=4096"}, args...))
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	return cmd.Execute()
}

func TestAgentCommand_Stdout(t *testing.T) {
	got, d := stubAgent(t, nil, nil)
	if err := runAgentCommand(d, "--publish-interval=5s", "--resync-interval=10s", "--node-name=h1", "--proc-root=/p"); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Sink.(runtimewatch.JSONSink); !ok || got.Keep != nil || got.Cgroups == nil ||
		got.NodeName != "h1" || got.AgentVersion != "v9" || got.PublishInterval != 5*time.Second ||
		got.ResyncInterval != 10*time.Second || got.Proc != (runtimewatch.OSProcFS{Root: "/p"}) {
		t.Fatalf("config = %+v", got)
	}
	if err := runAgentCommand(d, "--cgroup-root="); err != nil || got.Cgroups != nil {
		t.Fatalf("empty --cgroup-root: err %v, cgroups %v", err, got.Cgroups)
	}
}

func TestAgentCommand_Kubernetes(t *testing.T) {
	got, d := stubAgent(t, nil, nil)
	if err := runAgentCommand(d, "--sink=kubernetes", "--node-name=n1", "--namespace=pacto-system"); err != nil {
		t.Fatal(err)
	}
	s, ok := got.Sink.(*kubesink.Sink)
	if !ok || s.Name != "n1" || s.Namespace != "pacto-system" {
		t.Fatalf("sink = %+v", got.Sink)
	}
	if !got.Keep(runtimefacts.Identity{Kind: runtimefacts.KindPod}) || got.Keep(runtimefacts.Identity{Kind: runtimefacts.KindUnit}) {
		t.Fatal("the kubernetes sink keeps pods only")
	}
}

func TestAgentCommand_Errors(t *testing.T) {
	boom := errors.New("boom")
	for name, tc := range map[string]struct {
		args           []string
		collector, dyn error
		want           string
	}{
		"bad publish":          {args: []string{"--publish-interval=1500ms"}, want: "--publish-interval"},
		"bad resync":           {args: []string{"--resync-interval=0s"}, want: "--resync-interval"},
		"unknown sink":         {args: []string{"--sink=kafka"}, want: "--sink must be one of"},
		"kubernetes no node":   {args: []string{"--sink=kubernetes", "--node-name=", "--namespace=x"}, want: "--node-name and --namespace"},
		"kubernetes no client": {args: []string{"--sink=kubernetes", "--node-name=n", "--namespace=x"}, dyn: boom, want: "kubernetes sink: boom"},
		"unsupported":          {collector: boom, want: "boom"},
	} {
		t.Run(name, func(t *testing.T) {
			_, d := stubAgent(t, tc.collector, tc.dyn)
			if err := runAgentCommand(d, tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// A bad interval flag is a clear startup error, never a time.NewTicker panic.
func TestValidateIntervals(t *testing.T) {
	for _, tc := range []struct {
		name            string
		publish, resync time.Duration
		wantErr         string
	}{
		{"defaults", 30 * time.Second, time.Minute, ""},
		{"zero resync", 30 * time.Second, 0, "--resync-interval"},
		{"negative resync", 30 * time.Second, -time.Second, "--resync-interval"},
		{"zero publish", 0, time.Minute, "--publish-interval"},
		{"sub-second publish", 500 * time.Millisecond, time.Minute, "--publish-interval"},
		{"fractional publish", 1500 * time.Millisecond, time.Minute, "--publish-interval"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateIntervals(tc.publish, tc.resync)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("validateIntervals(%s, %s) = %v, want error containing %q", tc.publish, tc.resync, err, tc.wantErr)
			}
		})
	}
}

func TestAgentCommand_EvalSinks(t *testing.T) {
	got, d := stubAgent(t, nil, nil)
	var socket string
	d.newDocker = func(s string) *evalsink.Docker { socket = s; return evalsink.NewDocker(s) }
	bindings := filepath.Join(t.TempDir(), "b.yaml")
	_ = os.WriteFile(bindings, []byte("- match: {unit: a.service}\n  contract: oci://r/a@sha256:"+strings.Repeat("a", 64)+"\n"), 0o600)

	if err := runAgentCommand(d, "--sink=local", "--docker", "--bindings="+bindings, "--stabilization-window=5s", "--enable-probing"); err != nil {
		t.Fatal(err)
	}
	s, ok := got.Sink.(*evalsink.Sink)
	if !ok || len(s.Sources) != 2 || s.Window != 5*time.Second || !s.Protocol || got.Keep != nil || socket != dockerSocket {
		t.Fatalf("local sink = %+v", got.Sink)
	}
	if ts, err := s.Sources[0](context.Background()); err != nil || len(ts) != 1 {
		t.Fatalf("bindings source = %v, %v", ts, err)
	}
	if err := s.Emit(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	if err := runAgentCommand(d, "--sink=evidence", "--bindings="+bindings, "--evidence-url=http://e", "--evidence-key=k",
		"--evidence-key-id=k1", "--evidence-producer=p"); err != nil {
		t.Fatal(err)
	}
	if s, ok := got.Sink.(*evalsink.Sink); !ok || len(s.Sources) != 1 || s.Emit == nil {
		t.Fatalf("evidence sink = %+v", got.Sink)
	}
}

func TestAgentCommand_EvalSinkErrors(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "local.yaml")
	_ = os.WriteFile(local, []byte("- match: {unit: a.service}\n  contract: ./a\n"), 0o600)
	bad := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(bad, []byte("- bogus: 1\n"), 0o600)
	ev := []string{"--sink=evidence", "--evidence-url=http://e", "--evidence-key=k", "--evidence-key-id=k1", "--evidence-producer=p"}
	for name, tc := range map[string]struct {
		args   []string
		want   string
		format string
	}{
		"no source":        {args: []string{"--sink=local"}, want: "--bindings or --docker"},
		"bad bindings":     {args: []string{"--sink=local", "--bindings=" + bad}, want: "bogus"},
		"bad format":       {args: []string{"--sink=local", "--docker"}, want: "unsupported output format", format: "yaml"},
		"evidence flags":   {args: []string{"--sink=evidence", "--docker"}, want: "--evidence-url"},
		"evidence a local": {args: append(ev, "--bindings="+local), want: "pinned as oci://"},
	} {
		t.Run(name, func(t *testing.T) {
			_, d := stubAgent(t, nil, nil)
			v := viper.New()
			v.Set(outputFormatKey, cmp.Or(tc.format, "text"))
			cmd := newAgentCommand(&app.Service{}, v, "v9", d)
			cmd.SetArgs(append([]string{"--ringbuf-size=4096"}, tc.args...))
			cmd.SetOut(&strings.Builder{})
			cmd.SetErr(&strings.Builder{})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPrintRound(t *testing.T) {
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	results := []evalsink.Result{
		{Target: "container/web", Contract: "/c", Status: "NonCompliant", Coverage: validation.Coverage{Evaluated: 1, Required: 1},
			Findings: []finding.Finding{
				{Code: "INTERFACE_ABSENT", Severity: "error", Subject: finding.SubjectRef{Kind: "interface", Name: "api"}, Message: "gone",
					EvidenceRefs: []finding.EvidenceRef{{Collector: evalsink.CollectorKernel}}},
				{Code: "EVIDENCE_MISSING", Severity: "unknown", Subject: finding.SubjectRef{Kind: "dependency", Name: "db"}, Message: "none"},
			}},
		{Target: "unit/x", Contract: "./x", Error: "no pacto.yaml"},
		{Target: "unit/y", Contract: "./y", Status: "Unknown", Notes: []string{"probes unsupported: why"}},
	}
	var text strings.Builder
	_ = printRound(&text, "text", agentRound{EvaluatedAt: at, Targets: results})
	for _, want := range []string{"2026-10-18T12:00:00Z\n", "container/web  NonCompliant  1/1 evaluated  /c",
		"[error] INTERFACE_ABSENT interface/api: gone (pacto-agent+ebpf)", "EVIDENCE_MISSING dependency/db: none\n", "unit/x  error  ./x: no pacto.yaml", "  - note: probes unsupported: why\n"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, text.String())
		}
	}
	var md strings.Builder
	_ = printRound(&md, "markdown", agentRound{EvaluatedAt: at, Mode: "ebpf", Final: true, Targets: results})
	if !strings.HasPrefix(md.String(), "### 2026-10-18T12:00:00Z  ebpf  final\n\n- **container/web") {
		t.Errorf("markdown:\n%s", md.String())
	}
	var js strings.Builder
	_ = printRound(&js, "json", agentRound{EvaluatedAt: at, Targets: results})
	var round agentRound
	if err := json.Unmarshal([]byte(js.String()), &round); err != nil || len(round.Targets) != 3 || !round.EvaluatedAt.Equal(at) {
		t.Fatalf("json %s: %v", js.String(), err)
	}
}

func TestEvidenceEmitter(t *testing.T) {
	svc := &app.Service{}
	kp, err := svc.GenerateKey(t.TempDir(), "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	var seqs []uint64
	status := http.StatusAccepted
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		env, _ := evidenceenvelope.Decode(body)
		seqs = append(seqs, env.Sequence)
		w.WriteHeader(status)
		_, _ = w.Write([]byte("nope\n"))
	}))
	defer srv.Close()
	pinned := "oci://r/a@sha256:" + strings.Repeat("a", 64)
	set := evidence.EvidenceSet{Subject: evidence.SubjectRef{Kind: "service", Name: "container/a"}, ContractRef: pinned,
		Source: evalsink.Source, ObservedAt: time.Now()}
	ok := evalsink.Result{Target: "container/a", Contract: pinned, Evidence: set}
	opts := app.SignOptions{KeyPath: kp.PrivateKeyPath, KeyID: kp.KeyID, ProducerID: "host"}

	emit := evidenceEmitter(svc, srv.URL, opts, 100)
	if err := emit(context.Background(), []evalsink.Result{ok, ok}); err != nil || !reflect.DeepEqual(seqs, []uint64{101, 102}) {
		t.Fatalf("err %v, sequences %v", err, seqs)
	}
	status = http.StatusForbidden
	err = emit(context.Background(), []evalsink.Result{
		ok,
		{Target: "unit/x", Error: "no pacto.yaml"},
		{Target: "unit/y", Contract: "./y"},
		{Target: "bad", Contract: pinned}, // an empty set does not sign
	})
	for _, want := range []string{"answered 403: nope", "unit/x: no pacto.yaml", "unit/y: contract ./y is not pinned", "bad: invalid evidence set"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
	if err := evidenceEmitter(svc, "://bad", opts, 0)(context.Background(), []evalsink.Result{ok}); err == nil {
		t.Error("a post that cannot be sent reported no error")
	}
}

func TestAgentCommand_DockerLabelFilter(t *testing.T) {
	_, d := stubAgent(t, nil, nil)
	var made *evalsink.Docker
	d.newDocker = func(s string) *evalsink.Docker { made = evalsink.NewDocker(s); return made }
	if err := runAgentCommand(d, "--sink=local", "--docker",
		"--docker-label-filter=com.docker.compose.project=demo", "--docker-label-filter=tier"); err != nil {
		t.Fatal(err)
	}
	if made == nil || !slices.Equal(made.Labels, []string{"com.docker.compose.project=demo", "tier"}) {
		t.Fatalf("docker labels = %+v", made)
	}
	bindings := filepath.Join(t.TempDir(), "b.yaml")
	_ = os.WriteFile(bindings, []byte("- match: {unit: a.service}\n  contract: oci://r/a@sha256:"+strings.Repeat("a", 64)+"\n"), 0o600)
	for _, args := range [][]string{
		{"--sink=local", "--bindings=" + bindings, "--docker-label-filter=a=b"},
		{"--sink=local", "--docker", "--docker-label-filter=", "--docker-label-filter=a"},
		{"--sink=local", "--docker", "--docker-label-filter==v"},
	} {
		if err := runAgentCommand(d, args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}

// An agent off the host's network says up front that loopback-probed workloads cannot be probed.
func TestAgentCommand_ForeignNetns(t *testing.T) {
	link := func(name, target string) string {
		root := t.TempDir()
		p := filepath.Join(root, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		return root
	}
	_, d := stubAgent(t, nil, nil)
	d.selfProc = runtimewatch.OSProcFS{Root: link("self/ns/net", "net:[2]")}
	bindings := filepath.Join(t.TempDir(), "b.yaml")
	_ = os.WriteFile(bindings, []byte("- match: {unit: a.service}\n  contract: ./a\n"), 0o600)
	v := viper.New()
	v.Set(outputFormatKey, "text")
	cmd := newAgentCommand(&app.Service{}, v, "v9", d)
	cmd.SetArgs([]string{"--ringbuf-size=4096", "--sink=local", "--bindings=" + bindings, "--proc-root=" + link("1/ns/net", "net:[1]")})
	var errOut strings.Builder
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil || !strings.Contains(errOut.String(), "run the agent with --network=host") {
		t.Fatalf("err %v\nstderr %s", err, errOut.String())
	}
}
