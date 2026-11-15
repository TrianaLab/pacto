package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/registry"
)

// statsTimeout bounds one run-record publish, so a slow registry never holds
// a pipeline. A var so tests can shorten it.
var statsTimeout = 30 * time.Second

// defaultPublishRunRecord publishes r to the stats repository repo with the
// shared keychain and insecure-host list, and returns "<repo>@<digest>".
func defaultPublishRunRecord(ctx context.Context, repo string, r runrecord.Record) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, statsTimeout)
	defer cancel()
	target, err := registry.Open(repo, oci.NewKeychain(oci.EnvCredentialOptions()), oci.EnvInsecureRegistries())
	if err != nil {
		return "", err
	}
	desc, err := registry.Publish(ctx, target, r)
	if err != nil {
		return "", err
	}
	return repo + "@" + desc.Digest.String(), nil
}

// runRecorder leaves an unsigned run record of a finished run, when asked to.
// It never changes the run's output or exit code: every problem is a warning.
type runRecorder struct {
	version string
	now     func() time.Time
	publish func(context.Context, string, runrecord.Record) (string, error)
}

func newRunRecorder(version string, d deps) *runRecorder {
	return &runRecorder{version: version, now: time.Now, publish: d.publishRunRecord}
}

// addStatsFlags adds the two opt-in run-record targets. They are plain flags,
// never bound to viper: viper reads ./pacto.yaml, the contract, as config.
func addStatsFlags(cmd *cobra.Command) {
	cmd.Flags().String("stats-registry", "", "publish an unsigned run record (statistics, never evidence) to this repository, for example ghcr.io/acme/pacto-stats; default $PACTO_STATS_REGISTRY")
	cmd.Flags().String("stats-file", "", "write the run record to this file for pacto stats publish, replacing the record it holds, so give each command its own file; default $PACTO_STATS_FILE")
	cmd.Flags().String("stats-context", "", "comma-separated context to include in the run record: repository, run-url, environment=<name>; nothing is included unless named")
}

// statsTargets is where the run record goes: each flag when set, even to
// empty, else its env.
func statsTargets(cmd *cobra.Command) (registry, file string) {
	return flagOrEnv(cmd, "stats-registry", "PACTO_STATS_REGISTRY"), flagOrEnv(cmd, "stats-file", "PACTO_STATS_FILE")
}

// flagOrEnv is the flag's value when it was set, even to empty, else env's.
func flagOrEnv(cmd *cobra.Command, flag, env string) string {
	if cmd.Flags().Changed(flag) {
		v, _ := cmd.Flags().GetString(flag)
		return v
	}
	return os.Getenv(env)
}

// revisions identifies a change: the service and both bundle digests. err is
// why a digest could not be computed; the run then records nothing.
type revisions struct {
	service, old, new string
	err               error
}

// hook fills revs once both bundles resolve. It is nil when recording is off,
// so a run without recording computes no digest.
func (r *runRecorder) hook(cmd *cobra.Command, revs *revisions) func(old, new *contract.Bundle) {
	if reg, file := statsTargets(cmd); reg == "" && file == "" {
		return nil
	}
	return func(old, new *contract.Bundle) {
		revs.service = new.Contract.Service.Name
		var oerr, nerr error
		revs.old, oerr = oci.BundleDigest(old)
		revs.new, nerr = oci.BundleDigest(new)
		revs.err = errors.Join(oerr, nerr)
	}
}

func diffRecord(revs revisions, res *app.DiffResult) *runrecord.Record {
	outcome := runrecord.OutcomePassed
	if res.Classification == "BREAKING" {
		outcome = runrecord.OutcomeFailed
	}
	return &runrecord.Record{Kind: runrecord.KindDiff, Service: revs.service, OldRevision: revs.old, NewRevision: revs.new,
		Classification: res.Classification, Outcome: outcome}
}

// impactRecord is the record of a contract change answer, nil for an event,
// which has no revisions and catches no change. The consumer counts are the
// answer's own disjoint partition, with the completeness and asOf beside them.
func impactRecord(revs revisions, res *impact.Result) *runrecord.Record {
	if res.Event != nil {
		return nil
	}
	by := map[string]int{}
	for _, b := range res.ConsumerTallies.ByOutcome {
		by[b.Key] = b.Count
	}
	asOf := res.AsOf.UTC()
	return &runrecord.Record{Kind: runrecord.KindImpact, Service: revs.service, OldRevision: revs.old, NewRevision: revs.new,
		Classification: res.Classification, Gate: &runrecord.Gate{Verdict: res.Gate, Reason: res.GateCode()},
		Consumers: &runrecord.Consumers{Incompatible: by[impact.OutcomeIncompatible], Unknown: by[impact.OutcomeUnknown],
			Compatible: by[impact.OutcomeCompatible], Unclassified: by[impact.OutcomeUnclassified], Blocking: res.Blocking()},
		Completeness: string(res.Completeness), AsOf: &asOf, Outcome: impactOutcomes[res.Gate]}
}

// impactOutcomes is the outcome of each impact gate verdict.
var impactOutcomes = map[string]string{
	impact.GateClear:      runrecord.OutcomePassed,
	impact.GateBlocked:    runrecord.OutcomeFailed,
	impact.GateUnverified: runrecord.OutcomeUnverified,
}

// publishRecord is the record of a publish that ran (never --plan). The
// revisions are versions, so running the same publish again is one change.
func publishRecord(res *publish.Result, err error) *runrecord.Record {
	p := res.Plan
	rec := &runrecord.Record{Kind: runrecord.KindPublish, Service: p.Service, OldRevision: p.Baseline, NewRevision: p.Version,
		Classification: p.Classification}
	switch {
	case err == nil && len(res.Actions) > 0:
		rec.Outcome = runrecord.OutcomePublished
	case err == nil:
		rec.Outcome = runrecord.OutcomeUnchanged
	case errors.Is(err, publish.ErrNotReady):
		rec.Outcome = runrecord.OutcomeRefused
		rec.Gate = &runrecord.Gate{Verdict: runrecord.VerdictRefused, Reason: refusalReason(p)}
	default:
		rec.Outcome = runrecord.OutcomeFailed
	}
	if err == nil { // a failed run may hold the digest of a bundle it never wrote
		rec.BundleDigest = res.Digests[publish.BundleItem]
		_, rec.DeployDigest, _ = strings.Cut(res.Deploy, "@")
	}
	return rec
}

// refusalReason names what refused a plan, in the order publish reads it: the
// first failed check, else the state of the first conflicting or unreadable item.
func refusalReason(p publish.Plan) string {
	for _, c := range p.Checks {
		if !c.Passed {
			return c.Name
		}
	}
	for _, it := range p.Items {
		if it.State == publish.Conflict || it.State == publish.Unreadable {
			return string(it.State)
		}
	}
	return ""
}

// save writes and publishes *rec when it is set and a target is. Deferred at
// the top of RunE, it runs after the return value is chosen and never touches
// it. revs, nil for publish, carries a digest error that drops the record.
func (r *runRecorder) save(cmd *cobra.Command, rec **runrecord.Record, revs *revisions) {
	reg, file := statsTargets(cmd)
	if *rec == nil || (reg == "" && file == "") {
		return
	}
	w := cmd.ErrOrStderr()
	if revs != nil && revs.err != nil {
		_, _ = fmt.Fprintf(w, "pacto: warning: run record not written: could not compute the bundle digest: %v\n", revs.err)
		return
	}
	out := **rec
	out.Version = runrecord.Version
	out.ID = runrecord.ID(out.Kind, out.Service, out.OldRevision, out.NewRevision)
	out.PactoVersion = r.version
	out.RecordedAt = r.now().UTC().Truncate(time.Second)
	spec, _ := cmd.Flags().GetString("stats-context")
	var warns []string
	out.Repository, out.CIRunURL, out.Environment, warns = statsContext(spec, os.Getenv)
	for _, msg := range warns {
		_, _ = fmt.Fprintf(w, "pacto: warning: run record context: %s\n", msg)
	}
	data, err := runrecord.Encode(out)
	if file != "" {
		var replaced *runrecord.Record
		if err := unlessErr(err, func() (werr error) { replaced, werr = writeRecordFile(file, data); return werr }); err != nil {
			_, _ = fmt.Fprintf(w, "pacto: warning: run record not written: %v\n", err)
		} else {
			if replaced != nil {
				_, _ = fmt.Fprintf(w, "pacto: warning: replaced the run record of %s %s in %s\n", replaced.Kind, replaced.Service, file)
			}
			_, _ = fmt.Fprintf(w, "pacto: run record written to %s\n", file)
		}
	}
	if reg != "" {
		at := ""
		err := unlessErr(err, func() (perr error) { at, perr = r.publish(cmd.Context(), reg, out); return perr })
		if err != nil {
			_, _ = fmt.Fprintf(w, "pacto: warning: run record not published: %v\n", err)
		} else {
			_, _ = fmt.Fprintf(w, "pacto: run record published to %s (unsigned statistics)\n", at)
		}
	}
}

// unlessErr is err when set, else what next returns.
func unlessErr(err error, next func() error) error {
	if err != nil {
		return err
	}
	return next()
}

// writeRecordFile writes data to path and returns the run record it replaced,
// if any. It refuses to overwrite a file that holds something other than a run
// record; an empty file holds nothing and is written like a missing one.
func writeRecordFile(path string, data []byte) (*runrecord.Record, error) {
	var replaced *runrecord.Record
	if f, err := os.Open(path); err == nil {
		old, rerr := io.ReadAll(io.LimitReader(f, runrecord.MaxRecordBytes+1))
		_ = f.Close()
		if rerr != nil {
			return nil, rerr
		}
		if len(old) > 0 {
			prev, perr := runrecord.Parse(old)
			if perr != nil {
				return nil, fmt.Errorf("%s holds something other than a run record; it was left untouched", path)
			}
			replaced = &prev
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return replaced, os.WriteFile(path, data, 0o644)
}

// statsContext reads the context fields spec names, each from the CI
// environment or given explicitly as name=value. A field spec does not name
// stays empty; a bad name or value is a warning and leaves its field out.
func statsContext(spec string, getenv func(string) string) (repo, runURL, env string, warnings []string) {
	for _, item := range strings.Split(spec, ",") {
		name, value, explicit := strings.Cut(strings.TrimSpace(item), "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !explicit {
			value = contextDefault(name, getenv)
		}
		if name == "" {
			if explicit {
				warnings = append(warnings, "a context item has no name; it was left out")
			}
			continue
		}
		var err error
		switch name {
		case "repository":
			repo = value
		case "run-url":
			runURL, err = cleanRunURL(value)
		case "environment":
			env = value
		default:
			err = fmt.Errorf("unknown context %q; use repository, run-url or environment", name)
		}
		if err == nil && value == "" {
			err = fmt.Errorf("%s has no value; it was left out", name)
		}
		if err != nil {
			warnings = append(warnings, err.Error())
		}
	}
	return repo, runURL, env, warnings
}

// contextDefault is the CI value of a bare context name: GitHub Actions,
// else GitLab CI. environment has no default.
func contextDefault(name string, getenv func(string) string) string {
	switch name {
	case "repository":
		return firstSet(getenv("GITHUB_REPOSITORY"), getenv("CI_PROJECT_PATH"))
	case "run-url":
		srv, repo, id := getenv("GITHUB_SERVER_URL"), getenv("GITHUB_REPOSITORY"), getenv("GITHUB_RUN_ID")
		if srv != "" && repo != "" && id != "" {
			return srv + "/" + repo + "/actions/runs/" + id
		}
		return getenv("CI_JOB_URL")
	}
	return ""
}

func firstSet(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// cleanRunURL keeps an http or https URL with a host and no userinfo, without
// its query and fragment. An error never echoes the URL, which may hold a secret.
func cleanRunURL(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return "", errors.New("run-url is not an http or https URL with a host; it was left out")
	case u.User != nil:
		return "", errors.New("run-url carries credentials; it was left out")
	}
	u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = "", false, "", ""
	return u.String(), nil
}
