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
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

func newFleetOfferingsCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "offerings",
		Short: "List the platform offerings: declared, inferred (--inferred) or the ones you may consume (--consumable)",
		Long: "Lists the declared platform offerings, with a drift line under an offering accepted from an inferred one whose " +
			"source changed or that Pacto cannot check, and how many inferred offerings --inferred lists. With --inferred it lists " +
			"the inferred offerings producers contributed instead: never declared, never consumable and never a gate, each with " +
			"its source object, producer, classes and the pacto declare offering:<name> command a person runs to accept it. " +
			"With --consumable it lists the offerings you may consume: accepted, with a closed parameter schema and visible to you, " +
			"each provider with its version, digest, publisher, classes, contract fields (set once, the same in every environment), " +
			"tunable fields with their bounds, guarantees and a snippet (in --output-format json) to add to a contract. " +
			"Acceptance covers the listed digest; run pacto lock to pin it. A partial answer leaves a missing offering unknown.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: noCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			inferred, _ := cmd.Flags().GetBool("inferred")
			consumable, _ := cmd.Flags().GetBool("consumable")
			limit, _ := cmd.Flags().GetInt("limit")
			offset, _ := cmd.Flags().GetInt("offset")
			f := fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Consumable: consumable, Limit: limit, Offset: offset}
			what := fleet.DeclarationDeclared
			switch {
			case inferred:
				f.Declaration, what = fleet.DeclarationInferred, fleet.DeclarationInferred
			case consumable:
				what = "consumable"
			}
			res, err := q.Entities(f)
			if err != nil {
				return err
			}
			detected, _ := cmd.Flags().GetString("detected")
			return printFleetOfferings(cmd, offeringsAnswer(q.SnapshotForSerialization(), res), what, detected, v.GetString(outputFormatKey))
		},
	}
	f := cmd.Flags()
	f.Bool("inferred", false, "list the inferred offerings producers contributed instead of the declared ones (pacto fleet entities --kind offering --declaration inferred)")
	f.Bool("consumable", false, "only the offerings you may consume: accepted, with a closed parameter schema, visible to you, each with its "+
		"version, digest, publisher, parameters, guarantees and a snippet; partial when Pacto could not check one. Acceptance covers the listed digest; run pacto lock to pin it")
	cmd.MarkFlagsMutuallyExclusive("inferred", "consumable")
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
			row.From = e.InferredOffering.Ref
		} else {
			row.From, row.AsOf, row.Drift = providerFacts(snap, e.Key)
		}
		// A consumable row is read from the provider acceptance judged, never
		// from one it left out.
		if c := e.Consumable; c != nil && len(c.Providers) > 0 {
			p := c.Providers[0]
			row.From, row.AsOf = strings.TrimSpace(p.Service.Key+" "+p.Version), p.Revision.FetchedAt
		}
		out.Offerings = append(out.Offerings, row)
	}
	return out
}

// newestRevision is s's newest readable revision; nil for none.
func newestRevision(snap *fleet.FleetSnapshot, s *fleet.ServiceRecord) *fleet.ContractRevision {
	var newest *fleet.ContractRevision
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

// printFleetOfferings prints res, a list of the what (declared, inferred or
// consumable) offerings.
func printFleetOfferings(cmd *cobra.Command, res *fleetOfferings, what, detected, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		if what == "consumable" {
			for _, o := range res.Offerings {
				printConsumable(w, o.EntityRef)
			}
		} else {
			printOfferingRows(w, res, detected)
		}
		if len(res.Offerings) == 0 {
			_, _ = fmt.Fprintln(w, noOfferings(what, res.Completeness))
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
		if what == "consumable" && res.Completeness == fleet.CompletenessComplete {
			noteLeftOut(cmd, res.Limitations)
		}
		return nil
	}, nil)
}

// noteLeftOut prints each provider a complete consumable answer left out
// and why, which warnPartial keeps for partial answers: an exclusion is
// never silent.
func noteLeftOut(cmd *cobra.Command, lims []fleet.Limitation) {
	for _, l := range lims {
		if l.Code == fleet.LimitationOfferingNotConsumable {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "note: [%s] %s\n", l.Code, l.Message)
		}
	}
}

// noOfferings is the line of an empty page: a partial answer leaves
// whether there are any unknown.
func noOfferings(what string, c fleet.Completeness) string {
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

// printConsumable prints, for each provider of a consumable offering row,
// what an agent needs to depend on it.
func printConsumable(w io.Writer, o fleet.EntityRef) {
	c := o.Consumable
	if c == nil {
		return
	}
	for _, p := range c.Providers {
		state := "declared"
		if p.Lifecycle == fleet.LifecycleDeprecated {
			state += ", deprecated"
		}
		_, _ = fmt.Fprintf(w, "%s  %s  %s  %s  %s, publisher %s\n", o.Label, serviceName(p.Service), p.Version, shortDigest(p.Digest), state, p.Publisher)
		var contract, tunable []string
		for _, x := range p.Parameters {
			path := x.Path
			if len(p.SchemaPaths) > 1 {
				path = x.Configuration + "." + path // several configurations may share a field name
			}
			if x.Tier == schemax.TierTunable {
				tunable = append(tunable, strings.TrimSpace(path+" "+boundsText(x)))
			} else {
				contract = append(contract, path)
			}
		}
		classes := strings.Join(p.Classes, ", ")
		if p.ClassesTruncated {
			classes += fmt.Sprintf("  (%d of %d shown)", len(p.Classes), p.ClassesTotal)
		}
		consumableLine(w, "classes", classes)
		if len(contract) > 0 {
			consumableLine(w, "contract", strings.Join(contract, ", ")+"  (set once, same in every environment)")
		}
		consumableLine(w, "tunable", strings.Join(tunable, ", "))
		if p.ParametersTruncated {
			consumableLine(w, "parameters", fmt.Sprintf("%d of %d shown", len(p.Parameters), p.ParametersTotal))
		}
		consumableLine(w, "guarantees", cmp.Or(guaranteesText(p.Guarantees), "declares no guarantees"))
		consumableLine(w, "snippet", "dependency and configuration values in --output-format json")
	}
	if c.ProvidersTruncated {
		_, _ = fmt.Fprintf(w, "  ... %d of %d providers shown\n", len(c.Providers), c.ProvidersTotal)
	}
}

// serviceName is a service reference's domain and name, the readable form of its key.
func serviceName(s fleet.EntityRef) string {
	if s.Domain == "" {
		return s.Label
	}
	return s.Domain + "/" + s.Label
}

// consumableLine prints one labelled line of a consumable provider; an
// empty value prints nothing.
func consumableLine(w io.Writer, label, value string) {
	if value == "" {
		return
	}
	_, _ = fmt.Fprintf(w, "  %-11s %s\n", label, value)
}

// boundsText is a parameter's numeric bounds: min-max, or each side alone.
func boundsText(p fleet.OfferingParameter) string {
	if p.Minimum != nil && p.Maximum != nil {
		return fmt.Sprintf("%g-%g", *p.Minimum, *p.Maximum)
	}
	var out []string
	for _, b := range []struct {
		op string
		v  *float64
	}{{">=", p.Minimum}, {">", p.ExclusiveMinimum}, {"<=", p.Maximum}, {"<", p.ExclusiveMaximum}} {
		if b.v != nil {
			out = append(out, fmt.Sprintf("%s%g", b.op, *b.v))
		}
	}
	return strings.Join(out, " ")
}

// guaranteesText is each interface's guarantees, "sql: timeout 5s".
func guaranteesText(gs []fleet.ConsumableGuarantee) string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.Interface + ": " + g.Text
	}
	return strings.Join(out, "; ")
}
