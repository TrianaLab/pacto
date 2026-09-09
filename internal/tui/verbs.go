package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// Verb is one keybound action. Argv is the invocation a reader could have typed
// instead: it drives the yank verb, the write confirmation and the boundary
// test's coverage claim, so every verb builds it in exactly one place and the
// Run closure uses that same builder. Four verbs take an argument no single
// selection carries — a registry reference, a plugin name, the left-hand side of
// a comparison; their Argv shows it as a placeholder, which is the honest shape
// of the line rather than an approximation of it.
type Verb struct {
	Key   string
	Help  string
	Write bool
	// Applies reports why the verb cannot run against sel, and "" when it can.
	// It returns the reason rather than a bool so the rejection the reader sees
	// is written next to the condition that produced it: the predicates key on
	// what the bundle IS, and a message assembled elsewhere from sel.Kind blames
	// the kind for a locality problem.
	Applies func(sel Selection) string
	Argv    func(c *Context, sel Selection) []string
	Run     func(c *Context, sel Selection) tea.Cmd
}

// noBundle is the one rejection that really is about the kind: an owner and a
// source are aggregations over contracts, not contracts.
func noBundle(sel Selection) string { return "this " + string(sel.Kind) + " has no bundle" }

// hasBundle is the Applies predicate for every verb that takes either a
// directory or a registry reference.
func hasBundle(sel Selection) string {
	if sel.Ref == "" {
		return noBundle(sel)
	}
	return ""
}

// always is the Applies predicate for verbs that work on any selection.
func always(Selection) string { return "" }

// verbList is the verb table. It drives dispatch, the help screen and the yank
// verb from one definition, so none of the three can drift.
func verbList(c *Context) []Verb {
	vs := []Verb{
		{
			Key:     "y",
			Help:    "copy the equivalent pacto command",
			Applies: always,
			Argv:    yankArgv,
			Run:     verbYank,
		},
		{
			Key: "v", Help: "validate the selected bundle", Applies: hasBundle,
			Argv: func(_ *Context, s Selection) []string { return []string{"pacto", "validate", s.Ref} },
			Run:  verbValidate,
		},
		{
			Key: "E", Help: "explain the selected bundle", Applies: hasBundle,
			Argv: func(_ *Context, s Selection) []string { return []string{"pacto", "explain", s.Ref} },
			Run:  verbExplainLocal,
		},
		{
			Key: "e", Help: "explain the selection from the fleet's point of view", Applies: explainable,
			Argv: func(_ *Context, s Selection) []string {
				return []string{"pacto", "fleet", "explain", explainSubject(s)}
			},
			Run: verbFleetExplain,
		},
		{
			// Local, not hasBundle: a lock file lives beside a bundle on disk, and
			// app.Lock refuses a registry reference outright (internal/app/lock.go:57).
			Key: "l", Help: "check the selected bundle's lock file", Applies: hasLocalBundle,
			Argv: func(_ *Context, s Selection) []string { return []string{"pacto", "lock", "--check", s.Ref} },
			Run:  verbLockCheck,
		},
		{
			Key: "d", Help: "diff two selections (press twice)", Applies: hasBundle,
			Argv: func(c *Context, s Selection) []string {
				return []string{"pacto", "diff", oldOrPlaceholder(c.pendingDiff.Ref), s.Ref}
			},
			Run: verbDiff,
		},
		{
			Key: "i", Help: "impact of one selection on another (press twice)", Applies: hasBundle,
			Argv: func(c *Context, s Selection) []string {
				return []string{"pacto", "impact", oldOrPlaceholder(c.pendingImpact.Ref), s.Ref}
			},
			Run: verbImpact,
		},
		{
			Key: "g", Help: "open the selection's neighborhood graph", Applies: graphable,
			Argv: func(_ *Context, s Selection) []string {
				return append([]string{"pacto", "fleet", "graph"}, graphRoot(s)...)
			},
			// Navigation rather than output, but it lives in the table so the
			// help screen and the yank verb see it like everything else.
			Run: func(c *Context, s Selection) tea.Cmd {
				return push(newGraphScreen(c, fleet.EntityRef{
					Kind:  s.Kind,
					Key:   s.Key,
					Label: s.Label,
					// The graph screen is itself a selection a verb can run
					// against, so the ref carries everything a verb reads off a
					// row. Dropping ParentService here would leave e explaining
					// nothing on the graph of a revision.
					ParentService: s.ParentService,
					Version:       s.Version,
				}))
			},
		},
	}
	if c.ReadOnly {
		return vs
	}
	return append(vs, writeVerbs()...)
}

// writeVerbs are the four verbs that change something. Each is offered only
// when the selection can carry it, and each goes through runWrite, so there is
// exactly one confirmation path and no way to add a write that skips it.
func writeVerbs() []Verb {
	return []Verb{
		{
			Key: "p", Help: "push the selected bundle to a registry", Write: true, Applies: hasLocalBundle,
			Argv: func(_ *Context, s Selection) []string { return pushArgv(refPlaceholder, s) },
			Run: func(c *Context, s Selection) tea.Cmd {
				return promptFor("Registry reference to push "+s.Label+" to (oci://...):", func(ref string) tea.Cmd {
					return runWrite(c, "Push "+s.Ref+" to "+ref+"?", pushArgv(ref, s))
				})
			},
		},
		{
			Key: "P", Help: "pull the selected revision", Write: true, Applies: hasRemoteRef,
			Argv: pullArgv,
			Run: func(c *Context, s Selection) tea.Cmd {
				// pull clobbers its destination and has no --force to soften it,
				// so the prompt names the destination rather than the source.
				return runWrite(c, "Pull "+s.Ref+" into ./"+pullDir(s.Ref)+"? This overwrites that directory.", pullArgv(c, s))
			},
		},
		{
			Key: "L", Help: "update the selected bundle's lock file", Write: true, Applies: hasLocalBundle,
			Argv: lockUpdateArgv,
			Run: func(c *Context, s Selection) tea.Cmd {
				return runWrite(c, "Rewrite the lock file for "+s.Label+"?", lockUpdateArgv(c, s))
			},
		},
		{
			Key: "G", Help: "run a generate plugin over the selected bundle", Write: true, Applies: hasLocalBundle,
			Argv: func(_ *Context, s Selection) []string { return generateArgv(pluginPlaceholder, s) },
			Run: func(c *Context, s Selection) tea.Cmd {
				return promptFor("Plugin to run over "+s.Label+" (executes pacto-plugin-<name>):", func(plugin string) tea.Cmd {
					// generate executes a plugin binary. The prompt says so, because
					// confirming this is confirming arbitrary code.
					return runWrite(c, "Run pacto-plugin-"+plugin+" over "+s.Ref+"? This executes that binary.", generateArgv(plugin, s))
				})
			},
		},
	}
}

// The two arguments no selection can supply. A contract declares neither a
// registry to publish to nor a plugin to run, so the reader is asked, and the
// line the help screen and the yank verb show carries the placeholder rather
// than a value invented for them.
const (
	refPlaceholder    = "<ref>"
	pluginPlaceholder = "<plugin>"
)

// pushArgv is the one definition of what p runs. The positional is the
// DESTINATION and the bundle rides on -p (internal/cli/push.go:55); reading the
// command left to right suggests the opposite, which is how the bundle in the
// TUI's own working directory ended up being published under someone else's ref.
func pushArgv(ref string, sel Selection) []string {
	return []string{"pacto", "push", ref, "-p", sel.Ref}
}

// generateArgv is the one definition of what G runs. The first positional is a
// plugin NAME, resolved to a pacto-plugin-<name> binary on PATH; the bundle is
// the optional second (internal/cli/generate.go:16).
func generateArgv(plugin string, sel Selection) []string {
	return []string{"pacto", "generate", plugin, sel.Ref}
}

// pullArgv names the destination explicitly. pull's -o default is the service
// name read out of the bundle it has just downloaded, so without this the
// confirmation could not say where the files are about to land.
func pullArgv(_ *Context, sel Selection) []string {
	return []string{"pacto", "pull", sel.Ref, "-o", pullDir(sel.Ref)}
}

// lockUpdateArgv is the one definition of what L runs. lock takes a directory,
// never a reference, which is why L is a local-only verb.
func lockUpdateArgv(_ *Context, sel Selection) []string {
	return []string{"pacto", "lock", "--update", sel.Ref}
}

// pullDir is the directory pull will write into: the last segment of the
// repository path with any tag or digest cut off. That is the service name for
// every ref that follows the convention and a legal directory name for the rest.
func pullDir(ref string) string {
	r := strings.TrimPrefix(ref, "oci://")
	if i := strings.LastIndex(r, "/"); i >= 0 {
		r = r[i+1:]
	}
	if i := strings.IndexAny(r, ":@"); i >= 0 {
		r = r[:i]
	}
	return r
}

// hasRemoteRef is the Applies predicate for pull: a local directory is already
// here, so offering to pull it is nonsense.
func hasRemoteRef(sel Selection) string {
	if sel.Ref == "" {
		return noBundle(sel)
	}
	if sel.Local {
		return "this bundle is already a directory on disk"
	}
	return ""
}

// hasLocalBundle is the Applies predicate for the verbs that read or write
// files beside a bundle, which needs a directory rather than a reference.
func hasLocalBundle(sel Selection) string {
	if sel.Ref == "" {
		return noBundle(sel)
	}
	if !sel.Local {
		return "this bundle is a registry reference, not a directory on disk"
	}
	return ""
}

// graphable is the Applies predicate for g. A neighborhood roots at a service,
// a revision or a target and nothing else (pkg/fleet/neighborhood.go:514), so on
// an owner or a source the verb could only ever open a screen showing an error.
func graphable(sel Selection) string {
	switch sel.Kind {
	case fleet.KindOwner, fleet.KindSource:
		return "a graph roots at a service, a revision or a target"
	}
	return ""
}

// explainable is the Applies predicate for e, and the same shape as graphable:
// Query.Explain resolves its subject as a service then as a target
// (pkg/fleet/query.go:908) and an owner key or a source name is neither, so the
// verb could only ever open a screen showing a NotFoundError. The reason names
// the command that does take them, which is the line y already yanks on those
// rows (yank.go:38-47).
func explainable(sel Selection) string {
	switch sel.Kind {
	case fleet.KindOwner, fleet.KindSource:
		return "fleet explain takes a service or a target — press y for the fleet search line"
	}
	return ""
}

// explainSubject is the one definition of what e explains, read by both the
// verb's Argv and its Run so the yanked line and the query cannot drift.
//
// A revision key is not a subject Query.Explain resolves, so a revision explains
// the service it is a revision OF: ParentService is that service's canonical,
// domain-qualified key and is already on the row (pkg/fleet/product.go:184), so
// this costs no second query. A service and a target are subjects in their own
// right and pass their key through.
func explainSubject(sel Selection) string {
	if sel.Kind == fleet.KindRevision {
		return sel.ParentService
	}
	return sel.Key
}

// graphRoot is how fleet graph names a root. Only a service is positional; a
// revision and a target each have their own flag (internal/cli/fleet.go:255).
func graphRoot(sel Selection) []string {
	switch sel.Kind {
	case fleet.KindRevision:
		return []string{"--revision", sel.Key}
	case fleet.KindTarget:
		return []string{"--target", sel.Key}
	}
	return []string{sel.Key}
}

// oldPlaceholder is the left-hand side of a comparison no single selection can
// supply, spelled the way diff and impact spell it in their own usage strings
// (internal/cli/diff.go:14, internal/cli/impact.go:21).
const oldPlaceholder = "<old>"

// oldOrPlaceholder names the left-hand side d and i advertise: the armed
// selection once there is one, and the metavar until then. Substituting the
// CURRENT selection there would advertise "pacto diff /tmp/svc /tmp/svc", which
// runs, exits 0 and reports NON_BREAKING — a bundle compared with itself,
// dressed as an answer. A line that fails is better than one that lies.
func oldOrPlaceholder(ref string) string {
	if ref == "" {
		return oldPlaceholder
	}
	return ref
}

// dispatchVerb routes a key to a verb. It is called only by the screens that
// carry a selection, so a confirmation screen or a focused filter never runs a
// verb by accident.
func dispatchVerb(c *Context, s screen, k tea.KeyPressMsg) (tea.Cmd, bool) {
	sel, ok := selectionOf(c, s)
	key := k.String()
	for _, v := range verbList(c) {
		if v.Key != key {
			continue
		}
		if !ok {
			return status("nothing selected"), true
		}
		if why := v.Applies(sel); why != "" {
			return status(v.Help + " does not apply: " + why), true
		}
		return v.Run(c, sel), true
	}
	return nil, false
}

// selectionOf resolves the screen's highlighted entity into a runnable
// Selection. A screen with no selection reports ok=false.
func selectionOf(c *Context, s screen) (Selection, bool) {
	sr, ok := s.(interface {
		selected() (fleet.EntityRef, bool)
	})
	if !ok {
		return Selection{}, false
	}
	ref, ok := sr.selected()
	if !ok {
		return Selection{}, false
	}
	sel, err := resolveSelection(c, ref)
	if err != nil {
		return Selection{}, false
	}
	return sel, true
}

func status(text string) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: text} }
}

// runRead opens an output screen and runs fn against it on a background
// goroutine. fn appends through the screen's id so late output from a
// superseded run is discarded rather than mixed in.
func runRead(c *Context, title string, fn func(o *outputScreen) error) tea.Cmd {
	o := newOutputScreen(title)
	return tea.Batch(
		push(o),
		o.sp.Tick,
		func() tea.Msg {
			err := fn(o)
			c.Send.send(outputDoneMsg{id: o.id, err: err})
			return nil
		},
	)
}

// emit posts one block of text into an output screen, one line per message so
// the pane fills progressively rather than all at once at the end.
func emit(c *Context, o *outputScreen, text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		c.Send.send(outputLineMsg{id: o.id, line: line})
	}
}

// onDep is the only safe body for an app-layer OnDepResolved hook: it fires
// from several goroutines at once, so it may do nothing but post a message.
// The id is what keeps a superseded run's late progress out of the current pane.
func onDep(c *Context, id int) func() {
	return func() { c.Send.send(depResolvedMsg{id: id}) }
}

func verbValidate(c *Context, sel Selection) tea.Cmd {
	return runRead(c, "validate "+sel.Label, func(o *outputScreen) error {
		// Unlike the other read verbs, this one renders whatever came back and
		// reports the error alongside it rather than instead of it. Validate never
		// returns an error for a bad contract — the verdict is in the result, and
		// returning early on err would call every invalid bundle fine. The only
		// errors it does return are for a resolved bundle with no readable
		// pacto.yaml (internal/app/validate.go:68,71), which nothing that resolved
		// in the first place can be, so an early return would also add a branch no
		// input can take. renderValidate reports a nil result honestly.
		res, err := c.Svc.Validate(c.Ctx, app.ValidateOptions{Path: sel.Ref})
		emit(c, o, renderValidate(res))
		return err
	})
}

func verbExplainLocal(c *Context, sel Selection) tea.Cmd {
	return runRead(c, "explain "+sel.Label, func(o *outputScreen) error {
		res, err := c.Svc.Explain(c.Ctx, app.ExplainOptions{Path: sel.Ref})
		if err != nil {
			return err
		}
		emit(c, o, renderExplain(res))
		return nil
	})
}

func verbFleetExplain(c *Context, sel Selection) tea.Cmd {
	// The title names the subject rather than the selection: on a revision row
	// the answer is about the parent service, and titling it with the revision
	// label would tell the reader they are reading something they are not.
	subject := explainSubject(sel)
	return runRead(c, "fleet explain "+subject, func(o *outputScreen) error {
		res, err := c.Query.Explain(subject)
		if err != nil {
			return err
		}
		emit(c, o, renderFleetExplain(res))
		return nil
	})
}

func verbLockCheck(c *Context, sel Selection) tea.Cmd {
	return runRead(c, "lock --check "+sel.Label, func(o *outputScreen) error {
		res, err := c.Svc.Lock(c.Ctx, app.LockOptions{
			Path: sel.Ref, Check: true, OnDepResolved: onDep(c, o.id),
		})
		if err != nil {
			return err
		}
		emit(c, o, renderLock(res))
		return nil
	})
}

func verbDiff(c *Context, sel Selection) tea.Cmd {
	if c.pendingDiff.Ref == "" {
		c.pendingDiff = sel
		return status("diff armed on " + sel.Label + " — select the other side and press d again")
	}
	old := c.pendingDiff
	c.pendingDiff = Selection{}
	return runRead(c, "diff", func(o *outputScreen) error {
		res, err := c.Svc.Diff(c.Ctx, app.DiffOptions{OldPath: old.Ref, NewPath: sel.Ref})
		if err != nil {
			return err
		}
		emit(c, o, renderDiff(res))
		return nil
	})
}

func verbImpact(c *Context, sel Selection) tea.Cmd {
	if c.pendingImpact.Ref == "" {
		c.pendingImpact = sel
		return status("impact armed on " + sel.Label + " — select the new revision and press i again")
	}
	old := c.pendingImpact
	c.pendingImpact = Selection{}
	return runRead(c, "impact", func(o *outputScreen) error {
		// ImpactWithSnapshot binds the answer to the snapshot on screen. Impact
		// would build a second one, and then the blast radius shown would not be
		// the blast radius over the fleet the reader is looking at.
		res, err := c.Svc.ImpactWithSnapshot(c.Ctx, app.ImpactOptions{
			OldPath: old.Ref, NewPath: sel.Ref, Fleet: c.Fleet,
		}, c.Snapshot)
		if err != nil {
			return err
		}
		emit(c, o, renderImpact(res))
		return nil
	})
}

// VerbCommands reports which cobra commands the verb table covers, as
// space-joined paths. It is derived from each verb's Argv rather than written
// out by hand, so a verb that changes what it runs cannot leave the coverage
// claim behind. internal/cli asserts on this to prove no command was forgotten.
func VerbCommands() []string {
	// A zero Context is enough: Argv reads only the selection, and the two-part
	// verbs advertise their metavar for the side no selection supplies.
	c := &Context{}
	// One selection per shape the table branches on: locality splits push and
	// lock from pull, and a bundle-less kind is what sends yank down its own
	// switch. A verb is counted only for a selection it would really be offered
	// for, so this reports what the TUI can run rather than what the table can
	// print. Each is a whole row, ParentService included, because a real one is.
	sels := []Selection{
		{Kind: fleet.KindRevision, Key: "svc@1.0.0", Label: "svc", Ref: "/tmp/svc", Local: true, ParentService: "svc"},
		{Kind: fleet.KindRevision, Key: "svc@1.0.0", Label: "svc", Ref: "oci://ghcr.io/acme/svc:1.0.0", ParentService: "svc"},
		{Kind: fleet.KindTarget, Key: "prod/Deployment/svc", Label: "svc", ParentService: "svc"},
		{Kind: fleet.KindOwner, Key: "team:x", Label: "x"},
	}
	seen := map[string]bool{}
	var out []string
	for _, sel := range sels {
		for _, v := range verbList(c) {
			if v.Applies(sel) != "" {
				continue
			}
			path := commandPath(v.Argv(c, sel))
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, path)
		}
	}
	return out
}

// commandPath strips argv down to its cobra path: everything after "pacto" up
// to the first flag or operand. "pacto lock --check ./svc" is the lock command,
// and "pacto fleet get revision k" is the fleet get command.
func commandPath(argv []string) string {
	if len(argv) < 2 || argv[0] != "pacto" {
		return ""
	}
	parts := []string{argv[1]}
	if len(argv) > 2 && isSubcommand(argv[1], argv[2]) {
		parts = append(parts, argv[2])
	}
	return strings.Join(parts, " ")
}

// isSubcommand reports whether child is a subcommand of parent rather than an
// operand. Fleet is the only command the verb table nests into.
func isSubcommand(parent, child string) bool {
	if parent == "fleet" {
		switch child {
		case "explain", "get", "graph", "reconcile", "search", "snapshot", "status":
			return true
		}
	}
	return false
}
