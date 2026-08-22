// Command obscheck asserts the operator-managed OBSERVATION packaging against a
// real cluster: the Deployment wiring the chart produced, and the Product facts
// the live dashboard reports for the sources that wiring configured.
//
// It exists so the shell harness stays THIN. These are semantic assertions over
// JSON — "this volume is backed by the declared claim and mounted read-only",
// "this observed edge is attributed to that source and to no other" — and they
// were previously ~90 lines of python embedded in the harness as here-strings.
// Embedded, they could not be unit tested, so the only way to learn that an
// assertion had stopped asserting anything was for a real cluster run to pass
// when it should have failed. Here the same claims are a typed decode with a
// test suite over recorded payloads (main_test.go), which is exactly the
// mutation check the embedded version could never have.
//
//	wiring    reads a Deployment JSON on stdin and proves the mounts,
//	          the backing objects and PACTO_DASHBOARD_TRACE_SOURCES
//	snapshot  polls the live Product API and proves the source states,
//	          the limitations and the attribution of the observed edge
//
// Both modes accumulate EVERY failure and report them together: a run that
// reports only the first wrong thing costs a full cluster bring-up per fix.
// snapshot additionally polls, because the dashboard rebuilds its snapshot on an
// interval and the facts become true at an unpredictable moment; the errors it
// prints on timeout are the last round's complete list.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func main() {
	if len(os.Args) < 2 {
		exit("usage: obscheck wiring|snapshot [flags]")
	}
	var errs []string
	var err error
	switch os.Args[1] {
	case "wiring":
		errs, err = runWiring(os.Args[2:], os.Stdin)
	case "snapshot":
		errs, err = runSnapshot(os.Args[2:])
	default:
		exit("unknown mode %q (use wiring or snapshot)", os.Args[1])
	}
	if err != nil {
		exit("%v", err)
	}
	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, strings.Join(errs, "\n"))
		os.Exit(1)
	}
}

func exit(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "obscheck: "+format+"\n", a...)
	os.Exit(2)
}

// --- shared flag types -------------------------------------------------------

// listFlag collects a repeatable string flag.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// --- wiring ------------------------------------------------------------------

// source is one declared observation source, as the harness passes it:
// NAME:pvc|configMap:BACKING_OBJECT:FILE.
type source struct {
	name, kind, backing, file string
}

func parseSource(spec string) (source, error) {
	f := strings.Split(spec, ":")
	if len(f) != 4 || f[0] == "" || f[2] == "" || f[3] == "" {
		return source{}, fmt.Errorf("source %q must be NAME:pvc|configMap:BACKING:FILE", spec)
	}
	if f[1] != "pvc" && f[1] != "configMap" {
		return source{}, fmt.Errorf("source %q: backing kind must be pvc or configMap", spec)
	}
	return source{name: f[0], kind: f[1], backing: f[2], file: f[3]}, nil
}

// deployment is the sliver of a Kubernetes Deployment this gate reads. Declared
// here rather than imported from client-go: the harness pipes in real API JSON,
// and decoding four fields of it must not pull an apimachinery dependency into a
// test binary.
type deployment struct {
	Spec struct {
		Template struct {
			Spec struct {
				Volumes []struct {
					Name                  string `json:"name"`
					PersistentVolumeClaim *struct {
						ClaimName string `json:"claimName"`
						ReadOnly  bool   `json:"readOnly"`
					} `json:"persistentVolumeClaim"`
					ConfigMap *struct {
						Name string `json:"name"`
					} `json:"configMap"`
				} `json:"volumes"`
				Containers []struct {
					VolumeMounts []struct {
						Name      string `json:"name"`
						MountPath string `json:"mountPath"`
						ReadOnly  bool   `json:"readOnly"`
					} `json:"volumeMounts"`
					Env []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"env"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

// traceSourcesEnv is the variable the chart renders the configured sources into.
const traceSourcesEnv = "PACTO_DASHBOARD_TRACE_SOURCES"

// volPrefix is the chart's naming rule for an observation volume and its mount.
const volPrefix = "obs-"

func runWiring(args []string, stdin io.Reader) ([]string, error) {
	fs := flag.NewFlagSet("wiring", flag.ContinueOnError)
	var specs, absent listFlag
	mountRoot := fs.String("mount-root", "/var/lib/pacto/observation", "the root every source is mounted under")
	fs.Var(&specs, "source", "a declared source, NAME:pvc|configMap:BACKING:FILE (repeatable)")
	fs.Var(&absent, "absent", "a source name that must leave NO wiring behind (repeatable)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	var sources []source
	for _, spec := range specs {
		s, err := parseSource(spec)
		if err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("read the deployment on stdin: %w", err)
	}
	var d deployment
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("decode the deployment on stdin: %w", err)
	}
	return checkWiring(d, sources, absent, *mountRoot), nil
}

func checkWiring(d deployment, sources []source, absent []string, root string) []string {
	spec := d.Spec.Template.Spec
	if len(spec.Containers) == 0 {
		return []string{"the deployment has no containers"}
	}
	c := spec.Containers[0]

	volumes := map[string]int{} // name -> index into spec.Volumes
	for i, v := range spec.Volumes {
		volumes[v.Name] = i
	}
	mounts := map[string]int{}
	for i, m := range c.VolumeMounts {
		mounts[m.Name] = i
	}
	env := map[string]string{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}

	var errs []string
	for _, s := range sources {
		vol := volPrefix + s.name
		switch i, ok := volumes[vol]; {
		case !ok:
			errs = append(errs, vol+" has no volume")
		case s.kind == "pvc":
			pvc := spec.Volumes[i].PersistentVolumeClaim
			switch {
			case pvc == nil || pvc.ClaimName != s.backing:
				errs = append(errs, vol+" is not backed by the declared PVC "+s.backing)
			case !pvc.ReadOnly:
				// A writable mount of someone else's export is a Pacto that can
				// corrupt the evidence it reads.
				errs = append(errs, vol+" is not readOnly")
			}
		default:
			if cm := spec.Volumes[i].ConfigMap; cm == nil || cm.Name != s.backing {
				errs = append(errs, vol+" is not backed by the declared ConfigMap "+s.backing)
			}
		}
		i, ok := mounts[vol]
		switch m := c.VolumeMounts; {
		case !ok:
			errs = append(errs, "missing mount "+vol)
		case !m[i].ReadOnly:
			errs = append(errs, vol+" is mounted writable")
		case m[i].MountPath != root+"/"+s.name:
			errs = append(errs, vol+" mounted at "+m[i].MountPath)
		}
	}

	for _, name := range absent {
		vol := volPrefix + name
		if _, ok := volumes[vol]; ok {
			errs = append(errs, name+" left an orphaned volume behind")
		}
		if _, ok := mounts[vol]; ok {
			errs = append(errs, name+" left an orphaned mount behind")
		}
		if strings.Contains(env[traceSourcesEnv], name) {
			errs = append(errs, name+" is still configured")
		}
	}

	// The configured value is compared WHOLE, not by substring: the sources are
	// rendered sorted by name, and a chart that emitted them in declaration order
	// would still contain every expected fragment.
	if len(sources) > 0 {
		if got, want := env[traceSourcesEnv], wantTraceSources(sources, root); got != want {
			errs = append(errs, fmt.Sprintf("%s=%q, want %q", traceSourcesEnv, got, want))
		}
	}
	return errs
}

// wantTraceSources renders what the chart must have configured: every source as
// NAME=MOUNTPATH/FILE, sorted by name, space separated.
func wantTraceSources(sources []source, root string) string {
	parts := make([]string, 0, len(sources))
	for _, s := range sources {
		parts = append(parts, fmt.Sprintf("%s=%s/%s/%s", s.name, root, s.name, s.file))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// --- snapshot ----------------------------------------------------------------

type snapshotWant struct {
	available   []string // source ids that must be available
	unavailable []string // source ids that must be unavailable AND carry a limitation
	kinds       []string // a source of this kind must be available
	services    []string // service names that must be in the snapshot
	observed    []string // observed dependency edges, "FROM:TO" (name suffixes)
	attributed  []string // sources that must be named by every expected observed edge
	silent      []string // sources that must contribute to NO observed edge anywhere
}

func runSnapshot(args []string) ([]string, error) {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	var w snapshotWant
	base := fs.String("base", "http://127.0.0.1:8080", "dashboard base URL")
	timeout := fs.Duration("timeout", 90*time.Second, "how long to wait for the facts to become true")
	interval := fs.Duration("interval", 3*time.Second, "poll interval")
	fs.Var((*listFlag)(&w.available), "available", "a source id that must be available (repeatable)")
	fs.Var((*listFlag)(&w.unavailable), "unavailable", "a source id that must be unavailable, with a SOURCE_UNAVAILABLE limitation naming it (repeatable)")
	fs.Var((*listFlag)(&w.kinds), "kind-available", "a source KIND of which at least one must be available (repeatable)")
	fs.Var((*listFlag)(&w.services), "service", "a service name that must be in the snapshot (repeatable)")
	fs.Var((*listFlag)(&w.observed), "observed", "an observed dependency edge FROM:TO, matched on the service name suffix (repeatable)")
	fs.Var((*listFlag)(&w.attributed), "attributed", "a source every expected observed edge must be attributed to (repeatable)")
	fs.Var((*listFlag)(&w.silent), "silent", "a source that must have contributed to NO observed edge (repeatable)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	for _, e := range w.observed {
		if from, to, ok := strings.Cut(e, ":"); !ok || from == "" || to == "" {
			return nil, fmt.Errorf("observed edge %q must be FROM:TO", e)
		}
	}

	url := strings.TrimSuffix(*base, "/") + "/api/fleet/snapshot"
	deadline := time.Now().Add(*timeout)
	// Poll the ASSERTION, not the endpoint: the snapshot is rebuilt on an
	// interval, so a source that has not been read yet is a not-yet, not a
	// failure. The last round's errors are what a timeout reports — the first
	// round always writes them, whether the fetch or the check failed.
	var last []string
	for {
		if snap, err := fetchSnapshot(url); err != nil {
			last = []string{err.Error()}
		} else if last = checkSnapshot(snap, w); len(last) == 0 {
			return nil, nil
		}
		if time.Now().Add(*interval).After(deadline) {
			return last, nil
		}
		time.Sleep(*interval)
	}
}

func fetchSnapshot(url string) (*fleet.FleetSnapshot, error) {
	resp, err := http.Get(url) //nolint:gosec,noctx // a fixed localhost port-forward in an acceptance harness
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	var snap fleet.FleetSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return nil, fmt.Errorf("decode %s: %w", url, err)
	}
	return &snap, nil
}

func checkSnapshot(s *fleet.FleetSnapshot, w snapshotWant) []string {
	byID := map[string]fleet.SourceState{}
	for _, src := range s.Sources {
		byID[src.ID] = src
	}
	status := func(id string) string { return string(byID[id].Status) }

	var errs []string
	for _, id := range w.available {
		if _, ok := byID[id]; !ok {
			errs = append(errs, id+" is not a source in the snapshot")
		} else if byID[id].Status != fleet.SourceAvailable {
			errs = append(errs, id+" status="+status(id)+", want available")
		}
	}
	for _, id := range w.unavailable {
		if _, ok := byID[id]; !ok {
			errs = append(errs, id+" is not a source in the snapshot")
		} else if byID[id].Status != fleet.SourceUnavailable {
			errs = append(errs, id+" status="+status(id)+", want unavailable")
		}
		// A source that fails must be EXPLICIT unavailable knowledge: a status
		// alone leaves a consumer unable to say why the answer is incomplete.
		if !hasLimitation(s, fleet.LimitationSourceUnavailable, id) {
			errs = append(errs, "no "+fleet.LimitationSourceUnavailable+" limitation naming "+id)
		}
	}
	for _, kind := range w.kinds {
		if !anySource(s, func(src fleet.SourceState) bool {
			return src.Kind == kind && src.Status == fleet.SourceAvailable
		}) {
			errs = append(errs, "no available source of kind "+kind)
		}
	}
	for _, name := range w.services {
		found := false
		for _, rec := range s.Services {
			if rec != nil && rec.Name == name {
				found = true
				break
			}
		}
		if !found {
			errs = append(errs, "service "+name+" is not in the snapshot")
		}
	}

	for _, spec := range w.observed {
		from, to, _ := strings.Cut(spec, ":")
		rel := findObserved(s, from, to)
		if rel == nil {
			errs = append(errs, "no observed "+from+"->"+to+" edge reached the fleet")
			continue
		}
		named := map[string]bool{}
		for _, st := range rel.ObservedSources {
			named[st.Source] = true
		}
		for _, id := range w.attributed {
			if !named[id] {
				errs = append(errs, fmt.Sprintf("the observed %s->%s edge is not attributed to %s: %v",
					from, to, id, sourceNames(rel.ObservedSources)))
			}
		}
	}
	// Checked across the WHOLE snapshot, not just the expected edge: a source
	// that must have read nothing must not appear anywhere, including on an edge
	// nobody thought to assert.
	for _, id := range w.silent {
		for i := range s.Relationships {
			for _, st := range s.Relationships[i].ObservedSources {
				if st.Source == id {
					errs = append(errs, fmt.Sprintf("%s contributed evidence to the snapshot: %s->%s",
						id, s.Relationships[i].FromService, s.Relationships[i].ToService))
				}
			}
		}
	}
	return errs
}

// findObserved returns the observed dependency edge between two services, named
// by the SUFFIX of their keys: a fleet ServiceKey is domain-qualified, and the
// registry domain a scenario publishes to is not the fact under test.
func findObserved(s *fleet.FleetSnapshot, from, to string) *fleet.Relationship {
	for i := range s.Relationships {
		r := &s.Relationships[i]
		if r.Provenance == fleet.ProvenanceObserved &&
			strings.HasSuffix(string(r.FromService), from) &&
			strings.HasSuffix(string(r.ToService), to) {
			return r
		}
	}
	return nil
}

func hasLimitation(s *fleet.FleetSnapshot, code, srcID string) bool {
	for _, l := range s.Limitations {
		if l.Code == code && l.Source == srcID {
			return true
		}
	}
	return false
}

func anySource(s *fleet.FleetSnapshot, match func(fleet.SourceState) bool) bool {
	for _, src := range s.Sources {
		if match(src) {
			return true
		}
	}
	return false
}

func sourceNames(stats []fleet.ObservedSourceStat) []string {
	names := make([]string, 0, len(stats))
	for _, st := range stats {
		names = append(names, st.Source)
	}
	return names
}
