package cli

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func newFleetOfferingsCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "offerings",
		Short: "List the platform offerings, declared or with --inferred the inferred ones",
		Long: "Lists the declared platform offerings, with a drift line under an offering accepted from an inferred one whose " +
			"source changed or that Pacto cannot check, and how many inferred offerings --inferred lists. With --inferred it lists " +
			"the inferred offerings producers contributed instead: never declared, never consumable and never a gate, each with " +
			"its source object, producer, classes and the pacto declare offering:<name> command a person runs to accept it.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: noCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			inferred, _ := cmd.Flags().GetBool("inferred")
			limit, _ := cmd.Flags().GetInt("limit")
			offset, _ := cmd.Flags().GetInt("offset")
			f := fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Limit: limit, Offset: offset}
			if inferred {
				f.Declaration = fleet.DeclarationInferred
			}
			res, err := q.Entities(f)
			if err != nil {
				return err
			}
			detected, _ := cmd.Flags().GetString("detected")
			return printFleetOfferings(cmd, offeringsAnswer(q.SnapshotForSerialization(), res), inferred, detected, v.GetString(outputFormatKey))
		},
	}
	f := cmd.Flags()
	f.Bool("inferred", false, "list the inferred offerings producers contributed instead of the declared ones (pacto fleet entities --kind offering --declaration inferred)")
	f.Int("limit", 0, fmt.Sprintf("maximum results (0 = %d, capped at %d)", fleet.DefaultEntityLimit, fleet.MaxEntityLimit))
	f.Int("offset", 0, "result offset for paging")
	return cmd
}

// fleetOfferings is pacto fleet offerings' one document: a page of
// offerings with the answer's asOf and completeness.
type fleetOfferings struct {
	Offerings     []fleetOffering    `json:"offerings"`
	Total         int                `json:"total"`
	Count         int                `json:"count"`
	Truncated     bool               `json:"truncated"`
	NextOffset    *int               `json:"nextOffset,omitempty"`
	InferredTotal int                `json:"inferredTotal,omitempty"`
	AsOf          time.Time          `json:"asOf"`
	Completeness  fleet.Completeness `json:"completeness"`
	Limitations   []fleet.Limitation `json:"limitations,omitempty"`
}

// fleetOffering is one row: the entity reference, and on a declared
// offering the provider it is read from and the drift of every provider
// accepted from an inferred offering; on an inferred one the ref a person
// accepts it from.
type fleetOffering struct {
	fleet.EntityRef
	From  string                `json:"from,omitempty"`
	AsOf  *time.Time            `json:"asOf,omitempty"`
	Drift []fleet.OfferingDrift `json:"drift,omitempty"`
}

// offeringsAnswer is res as pacto fleet offerings answers it, with each
// row's facts read from snap, the snapshot res was answered from.
func offeringsAnswer(snap *fleet.FleetSnapshot, res *fleet.EntityList) *fleetOfferings {
	out := &fleetOfferings{Offerings: []fleetOffering{}, Total: res.Total, Count: res.Count, Truncated: res.Truncated, NextOffset: res.NextOffset,
		InferredTotal: res.InferredTotal, AsOf: res.Meta.AsOf, Completeness: res.Meta.Completeness, Limitations: res.Meta.Limitations}
	for _, e := range res.Entities {
		row := fleetOffering{EntityRef: e}
		if e.InferredOffering != nil {
			row.From = contributionFrom(snap, fleet.ServiceKey(e.Key))
		} else {
			row.From, row.AsOf, row.Drift = providerFacts(snap, e.Key)
		}
		out.Offerings = append(out.Offerings, row)
	}
	return out
}

// contributionFrom is the oci:// ref of the newest record of the inferred
// offering key, the --from a person accepts it with; empty when none is
// read.
func contributionFrom(snap *fleet.FleetSnapshot, key fleet.ServiceKey) string {
	newest := newestRevision(snap, snap.Services[key])
	if newest == nil || newest.ResolvedRef == "" {
		return ""
	}
	return "oci://" + contract.TrimOCIScheme(newest.ResolvedRef)
}

// newestRevision is s's newest readable revision; nil for none.
func newestRevision(snap *fleet.FleetSnapshot, s *fleet.ServiceRecord) *fleet.ContractRevision {
	var newest *fleet.ContractRevision
	if s == nil {
		return nil
	}
	for _, rk := range s.Revisions {
		if r := snap.Revisions[rk]; r != nil && (newest == nil || fleet.LessRevisionChrono(newest, r)) {
			newest = r
		}
	}
	return newest
}

// providerFacts are, for the declared offering, its first provider by key
// with its newest version and when that was read, and the drift of every
// provider accepted from an inferred offering.
func providerFacts(snap *fleet.FleetSnapshot, offering string) (string, *time.Time, []fleet.OfferingDrift) {
	var from string
	var asOf *time.Time
	var drift []fleet.OfferingDrift
	for _, k := range slices.Sorted(maps.Keys(snap.Services)) {
		s := snap.Services[k]
		if s.Offering != offering || s.Contribution != nil {
			continue
		}
		if s.OfferingDrift != nil {
			drift = append(drift, *s.OfferingDrift)
		}
		if from == "" {
			from, asOf = newestVersion(snap, s)
		}
	}
	return from, asOf, drift
}

// newestVersion is "<key> <version>" of s's newest revision and when it
// was read.
func newestVersion(snap *fleet.FleetSnapshot, s *fleet.ServiceRecord) (string, *time.Time) {
	newest := newestRevision(snap, s)
	if newest == nil {
		return string(s.Key), nil
	}
	return strings.TrimSpace(string(s.Key) + " " + newest.Version), newest.FetchedAt
}

func printFleetOfferings(cmd *cobra.Command, res *fleetOfferings, inferred bool, detected, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		printOfferingRows(w, res, detected)
		if len(res.Offerings) == 0 {
			_, _ = fmt.Fprintln(w, noOfferings(inferred, res.Completeness))
		}
		tail := ""
		if res.InferredTotal > 0 {
			tail = fmt.Sprintf("  (+%d inferred: pacto fleet offerings --inferred)", res.InferredTotal)
		}
		if res.Truncated {
			_, _ = fmt.Fprintf(w, "... %d of %d offerings shown\n", res.Count, res.Total)
		}
		_, _ = fmt.Fprintf(w, "asOf %s  completeness %s%s\n", res.AsOf.Format(time.RFC3339), res.Completeness, tail)
		warnPartial(cmd, fleet.Meta{Completeness: res.Completeness, AsOf: res.AsOf, Limitations: res.Limitations})
		return nil
	}, nil)
}

// noOfferings is the line of an empty page: a partial answer leaves
// whether there are any unknown.
func noOfferings(inferred bool, c fleet.Completeness) string {
	what := "declared"
	if inferred {
		what = "inferred"
	}
	if c == fleet.CompletenessPartial {
		return "No " + what + " offerings read: the answer is partial, so whether there are any is unknown"
	}
	return "No " + what + " offerings"
}

// printOfferingRows prints the table: one row per offering, then its
// producer and accept lines (inferred) or drift lines (declared).
func printOfferingRows(w io.Writer, res *fleetOfferings, detected string) {
	if len(res.Offerings) == 0 {
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "OFFERING\tPROVENANCE\tFROM\tCLASSES\tASOF")
	for _, o := range res.Offerings {
		if f := o.InferredOffering; f != nil {
			printInferredOffering(tw, o, f, detected)
			continue
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t-\t%s\n", o.Label, fleet.DeclarationDeclared, orDash(o.From), timeText(o.AsOf))
		for _, d := range o.Drift {
			_, _ = fmt.Fprintf(tw, "\t%s\n", driftLine(d))
		}
	}
	_ = tw.Flush()
}

// printInferredOffering prints an inferred offering's row, its producer and
// the command a person runs to accept it, or the offering it was accepted as.
func printInferredOffering(w io.Writer, o fleetOffering, f *fleet.InferredOfferingFacts, detected string) {
	from := orDash(f.From)
	if f.Generation > 0 {
		from += fmt.Sprintf(" gen %d", f.Generation)
	}
	classes := orDash(strings.Join(f.Classes, ", "))
	_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", f.Offering, fleet.DeclarationInferred, from, classes, timeText(f.AsOf))
	_, _ = fmt.Fprintf(w, "\t\tproducer %s\n", f.Producer)
	accept := "pacto declare offering:" + f.Offering + " --from " + cmp.Or(o.From, "<contribution>") + " --detected " +
		cmp.Or(detected, "k8s:<namespace>/<configmap>") + " --registry <registry> --version <version>"
	if f.AcceptedAs != nil {
		_, _ = fmt.Fprintf(w, "\taccepted as %s; accept again: %s\n", f.AcceptedAs.Key, accept)
		return
	}
	_, _ = fmt.Fprintf(w, "\taccept: %s --owner <team>\n", accept)
}

// driftLine is a declared offering's drift as one line.
func driftLine(d fleet.OfferingDrift) string {
	if d.State == fleet.DriftDrifted {
		return "drift: " + d.Reason
	}
	return "drift unknown: " + d.Reason
}

// timeText is t in RFC 3339, or a dash.
func timeText(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format(time.RFC3339)
}
