package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
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
	cmd.Flags().String("stats-file", "", "write the run record to this file for pacto stats publish; default $PACTO_STATS_FILE")
}

// statsTargets is where the run record goes: each flag, else its env.
func statsTargets(cmd *cobra.Command) (registry, file string) {
	registry, _ = cmd.Flags().GetString("stats-registry")
	file, _ = cmd.Flags().GetString("stats-file")
	if registry == "" {
		registry = os.Getenv("PACTO_STATS_REGISTRY")
	}
	if file == "" {
		file = os.Getenv("PACTO_STATS_FILE")
	}
	return registry, file
}

// revisions identifies a change: the service and both bundle digests.
type revisions struct{ service, old, new string }

// hook fills revs once both bundles resolve. It is nil when recording is off,
// so a run without recording computes no digest.
func (r *runRecorder) hook(cmd *cobra.Command, revs *revisions) func(old, new *contract.Bundle) {
	if reg, file := statsTargets(cmd); reg == "" && file == "" {
		return nil
	}
	return func(old, new *contract.Bundle) {
		revs.service = new.Contract.Service.Name
		// A digest error leaves that revision empty; the run still counts.
		revs.old, _ = oci.BundleDigest(old)
		revs.new, _ = oci.BundleDigest(new)
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

// publishRecord is the record of a publish that ran (never --plan). The
// revisions are versions, so running the same publish again is one change.
func publishRecord(res *publish.Result, err error) *runrecord.Record {
	p := res.Plan
	rec := &runrecord.Record{Kind: runrecord.KindPublish, Service: p.Service, OldRevision: p.Baseline, NewRevision: p.Version,
		BundleDigest: res.Digests[publish.BundleItem], Classification: p.Classification}
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
	if err == nil {
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
// the top of RunE, it runs after the return value is chosen and never touches it.
func (r *runRecorder) save(cmd *cobra.Command, rec **runrecord.Record) {
	reg, file := statsTargets(cmd)
	if *rec == nil || (reg == "" && file == "") {
		return
	}
	out := **rec
	out.Version = runrecord.Version
	out.ID = runrecord.ID(out.Kind, out.Service, out.OldRevision, out.NewRevision)
	out.PactoVersion = r.version
	out.RecordedAt = r.now().UTC().Truncate(time.Second)
	w := cmd.ErrOrStderr()
	data, err := runrecord.Encode(out)
	if file != "" {
		if err := unlessErr(err, func() error { return writeRecordFile(file, data) }); err != nil {
			_, _ = fmt.Fprintf(w, "pacto: warning: run record not written: %v\n", err)
		} else {
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

// writeRecordFile writes data to path, refusing to overwrite a file that
// holds something other than a run record.
func writeRecordFile(path string, data []byte) error {
	if f, err := os.Open(path); err == nil {
		old, rerr := io.ReadAll(io.LimitReader(f, runrecord.MaxRecordBytes+1))
		_ = f.Close()
		if rerr != nil {
			return rerr
		}
		if _, perr := runrecord.Parse(old); perr != nil {
			return fmt.Errorf("%s holds something other than a run record; it was left untouched", path)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
