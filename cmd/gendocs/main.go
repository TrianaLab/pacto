package main

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/cli"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/plugin"
)

//go:embed prose/front-matter.md
var frontMatter string

//go:embed prose/exit-codes.md
var exitCodes string

//go:embed prose/oci-version-resolution.md
var ociVersionResolution string

//go:embed prose/footer.md
var footer string

// notes holds additional documentation appended after the auto-generated
// content of each command, one prose/notes/<command>.md per command: the
// behavioural details, output examples and cross-references that cannot be
// derived from the cobra command definition alone.
//
//go:embed prose/notes
var notes embed.FS

func main() {
	if len(os.Args) > 1 && os.Args[1] == "registry" {
		if err := printRegistry(os.Stdout, filepath.Join("pkg", "evidence")); err != nil {
			fmt.Fprintf(os.Stderr, "error printing registry: %v\n", err)
			os.Exit(1)
		}
		return
	}

	outputPath := filepath.Join("docs", "cli-reference.md")
	if len(os.Args) > 1 {
		outputPath = os.Args[1]
	}

	keychain := oci.NewKeychain(oci.CredentialOptions{})
	store := oci.NewCachedStore(oci.NewClient(keychain))
	svc := app.NewService(store, &plugin.SubprocessRunner{})
	root := cli.NewRootCommand(svc, cli.VersionInfo{Version: "dev"})

	// Cobra materialises its built-in `completion` command inside ExecuteC, which
	// gendocs never calls — so without this the reference silently omits a command
	// `pacto --help` advertises.
	root.InitDefaultCompletionCmd()

	root.DisableAutoGenTag = true
	walk(root, func(cmd *cobra.Command) { cmd.DisableAutoGenTag = true })

	var buf bytes.Buffer
	buf.WriteString(frontMatter)

	// Generate the root command docs for global flags.
	var rootBuf bytes.Buffer
	if err := doc.GenMarkdownCustom(root, &rootBuf, noLink); err != nil {
		fmt.Fprintf(os.Stderr, "error generating root docs: %v\n", err)
		os.Exit(1)
	}
	sections := parseSections(rootBuf.String())
	buf.WriteString("## Global flags\n\n")
	if opts, ok := sections["Options"]; ok {
		buf.WriteString(opts)
		buf.WriteString("\n")
	}
	buf.WriteString("\n---\n\n")

	// Exit codes (static section). Cross-cutting and needed before any single
	// command's section, because no command's own note can say that the code
	// space is only {0,1} and therefore ambiguous.
	buf.WriteString(exitCodes)

	// OCI version resolution (static section).
	buf.WriteString(ociVersionResolution)

	// Generate docs for each subcommand and its own subcommands. A container
	// command whose only documented flag is --help is a reader dead end, so the
	// walk is recursive: `pacto fleet get` gets a section of its own.
	for _, cmd := range root.Commands() {
		if !cmd.IsAvailableCommand() || cmd.IsAdditionalHelpTopicCommand() {
			continue
		}
		emitCommand(&buf, cmd, cmd.Name(), 0)
		buf.WriteString("---\n\n")
	}

	// Drop the last command's trailing "---\n\n", since the footer starts with
	// one, and escape the placeholders last, over the finished document, so every
	// source of prose on this page -- cobra help text, notes, frontMatter and
	// footer -- goes through it.
	final := escapePlaceholders(strings.TrimSuffix(buf.String(), "---\n\n") + footer)

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating directory: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(outputPath, []byte(final), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated %s\n", outputPath)
}

func noLink(s string) string { return "" }

// placeholderTag matches a bare `<name>`, which a browser reads as an unknown
// HTML element and renders as nothing at all. Help text is full of them --
// `<keyId>.pub`, `oci://<repo>@sha256:<digest>` -- and inside a fenced block or a
// code span Markdown escapes them for us. In prose it does not, so the reference
// page silently published `oci://@sha256:` as the required format and told the
// reader to write the private seed to `.key`.
//
// The escaping happens here, on the generated page, and not in the help strings
// themselves: the same text is printed to a terminal by `--help`, where
// `&lt;keyId&gt;` would be the wrong thing to show.
var placeholderTag = regexp.MustCompile(`<([A-Za-z][A-Za-z0-9._-]*)>`)

// escapePlaceholders escapes every placeholderTag that is outside a fenced code
// block and outside an inline code span, leaving the rest byte-identical.
func escapePlaceholders(md string) string {
	lines := strings.Split(md, "\n")
	inFence := false
	for i, line := range lines {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.Contains(line, "<") {
			continue
		}
		// Split on backticks: even segments are prose, odd ones are code spans.
		// An unbalanced backtick leaves the tail treated as code, which errs
		// towards changing nothing.
		parts := strings.Split(line, "`")
		for j := 0; j < len(parts); j += 2 {
			parts[j] = placeholderTag.ReplaceAllString(parts[j], "&lt;$1&gt;")
		}
		lines[i] = strings.Join(parts, "`")
	}
	return strings.Join(lines, "\n")
}

// walk applies fn to cmd and every command beneath it.
func walk(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, sub := range cmd.Commands() {
		walk(sub, fn)
	}
}

// noRecurse names containers whose children add no Pacto-specific surface.
// `completion` is cobra's built-in and its four shell subcommands are byte-for-byte
// the same in every cobra CLI, so the reference documents the parent and leaves the
// shells to `pacto completion --help`.
var noRecurse = map[string]bool{"completion": true}

// emitCommand writes cmd's section, then recurses into its subcommands. path is
// the command words after "pacto"; depth 0 is a top-level command.
func emitCommand(buf *bytes.Buffer, cmd *cobra.Command, path string, depth int) {
	var cmdBuf bytes.Buffer
	if err := doc.GenMarkdownCustom(cmd, &cmdBuf, noLink); err != nil {
		fmt.Fprintf(os.Stderr, "error generating docs for %s: %v\n", path, err)
		os.Exit(1)
	}
	buf.WriteString(formatCommand(cmd, path, cmdBuf.String(), depth))

	if noRecurse[cmd.Name()] {
		return
	}
	for _, sub := range cmd.Commands() {
		if !sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
			continue
		}
		emitCommand(buf, sub, path+" "+sub.Name(), depth+1)
	}
}

// parseSections splits cobra-generated markdown into named sections.
// Section names are the H3 headings (e.g. "Synopsis", "Examples", "Options").
func parseSections(md string) map[string]string {
	sections := make(map[string]string)
	lines := strings.Split(md, "\n")

	var currentSection string
	var content strings.Builder

	for _, line := range lines {
		if heading, ok := strings.CutPrefix(line, "### "); ok {
			if currentSection != "" {
				sections[currentSection] = strings.TrimSpace(content.String())
			}
			currentSection = strings.TrimSpace(heading)
			content.Reset()
			continue
		}
		// Skip the top-level ## heading line.
		if strings.HasPrefix(line, "## ") {
			continue
		}
		if currentSection != "" {
			content.WriteString(line)
			content.WriteString("\n")
		}
	}
	if currentSection != "" {
		sections[currentSection] = strings.TrimSpace(content.String())
	}

	return sections
}

// formatCommand converts cobra-generated markdown for a subcommand into the
// desired format for the CLI reference page. depth sets the heading level, so a
// subcommand nests under its parent rather than competing with it.
func formatCommand(cmd *cobra.Command, path, md string, depth int) string {
	sections := parseSections(md)
	var sb strings.Builder

	fmt.Fprintf(&sb, "%s `pacto %s`\n\n", strings.Repeat("#", depth+2), path)

	// Write the description from Synopsis section.
	var desc, usage string
	if synopsis, ok := sections["Synopsis"]; ok {
		// Synopsis contains: description lines, then a code block with usage.
		parts := strings.SplitN(synopsis, "```", 3)
		desc = strings.TrimSpace(parts[0])
		if len(parts) >= 3 {
			usage = strings.TrimSpace(parts[1])
		}
	}
	// Cobra puts Short above the Synopsis heading and only repeats it inside when
	// Long is set, so a command with no Long would otherwise render as a bare
	// heading and a --help flag. Fall back to what `pacto <cmd> --help` shows.
	if desc == "" {
		desc = strings.TrimSpace(cmd.Short)
	}
	if usage == "" {
		usage = strings.TrimSpace(cmd.UseLine())
	}
	if desc != "" {
		sb.WriteString(desc)
		sb.WriteString("\n\n")
	}
	if usage != "" {
		sb.WriteString("```\n")
		sb.WriteString(usage)
		sb.WriteString("\n```\n")
	}

	// Write examples.
	if examples, ok := sections["Examples"]; ok {
		sb.WriteString("\n**Examples:**\n\n")
		sb.WriteString(examples)
		sb.WriteString("\n")
	}

	// Write command-specific flags (not inherited global flags).
	if opts, ok := sections["Options"]; ok {
		sb.WriteString("\n**Flags:**\n\n")
		sb.WriteString(opts)
		sb.WriteString("\n")
	}

	// Append additional notes for this command.
	if note, err := notes.ReadFile("prose/notes/" + path + ".md"); err == nil {
		sb.WriteString("\n")
		sb.Write(note) // each note file ends in its own newline
	}

	sb.WriteString("\n")
	return sb.String()
}
