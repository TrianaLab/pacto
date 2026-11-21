package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/ci"
)

// rekorNote is what a GitHub caller's keyless publish writes to a public log.
const rekorNote = "Keyless signing records the repository, workflow and commit in the public Rekor log; " +
	"a private repository that must not appear there sets the sign-key secret or declares publish.sign with a private log."

// ciInitResult is what pacto ci init wrote, or would write: Content is the
// whole resulting file, Lines the size of the caller it adds (the GitLab
// include entry is spliced into the existing file) and Notes what the caller
// discloses, so an agent reading JSON sees what a person reads.
type ciInitResult struct {
	Platform string   `json:"platform"`
	Path     string   `json:"path"`
	Lines    int      `json:"lines"`
	Content  string   `json:"content"`
	Written  bool     `json:"written"`
	Notes    []string `json:"notes,omitempty"`
}

// newCIInitCommand builds `pacto ci init [dir]`: write the CI caller that runs
// pacto ci into a repository's working tree.
func newCIInitCommand(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Write the CI caller that runs pacto ci",
		Long: "Writes the CI caller that runs pacto ci into the repository at dir (default .). " +
			"With a .github folder it writes .github/workflows/pacto.yml, five lines that call the pacto-actions reusable workflow " +
			"pinned by commit. With .gitlab-ci.yml it adds the pacto-actions GitLab component to its include list, " +
			"or moves the commit of the entry already there, and leaves every other byte of the file as it was. " +
			"--platform picks one when the repository has both or neither.\n\n" +
			"pacto ci init writes into the working tree only: you review and commit the file. " +
			"An existing GitHub caller with other content is replaced only with --force. " +
			"With --output-format json, content is the whole file written or to write and lines counts the lines of the caller it adds.\n\n" +
			"To plan a folder named init, run pacto ci ./init.",
		Example: `  # Write the caller for the CI platform this repository uses
  pacto ci init

  # Print the GitHub caller without writing it
  pacto ci init --platform github --dry-run`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			res, err := runCIInit(cmd, dir)
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), res, func() error { printCIInit(cmd, res); return nil }, nil)
		},
	}
	cmd.Flags().String("platform", "", "the CI platform to write the caller for, github or gitlab; default from the .github folder or .gitlab-ci.yml")
	cmd.Flags().Bool("dry-run", false, "print the caller without writing it")
	cmd.Flags().Bool("force", false, "replace a GitHub caller that has other content")
	return cmd
}

// runCIInit writes the caller for the platform of the repository at dir,
// unless --dry-run, and says what it wrote.
// Every read and write goes through a root at dir, so a dir that does not
// exist is an error and a symlink that leaves the repository is refused.
func runCIInit(cmd *cobra.Command, dir string) (*ciInitResult, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("the repository to write the caller into: %w", err)
	}
	defer func() { _ = root.Close() }()
	platform, _ := cmd.Flags().GetString("platform")
	if platform == "" {
		if platform, err = ci.DetectPlatform(root.FS()); err != nil {
			return nil, err
		}
	}
	path, content := ci.Caller(platform)
	if path == "" {
		return nil, fmt.Errorf("--platform %s: want github or gitlab", platform)
	}
	old, err := root.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	next, err := nextCallerFile(cmd, platform, path, old, content, exists)
	if err != nil {
		return nil, err
	}
	res := &ciInitResult{Platform: platform, Path: path, Lines: ci.Lines(content), Content: string(next)}
	if platform == "github" {
		res.Notes = []string{rekorNote}
	}
	if dry, _ := cmd.Flags().GetBool("dry-run"); dry || exists && bytes.Equal(old, next) {
		return res, nil
	}
	if err := root.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	res.Written = true
	return res, root.WriteFile(path, next, 0o644)
}

// nextCallerFile is the file at path once the caller is in it: the GitHub
// caller itself, refused over other content without --force, or old with the
// GitLab include entry.
func nextCallerFile(cmd *cobra.Command, platform, path string, old []byte, content string, exists bool) ([]byte, error) {
	if platform == "gitlab" {
		return ci.AddGitLabInclude(old, ci.GitLabIncludeURL())
	}
	if force, _ := cmd.Flags().GetBool("force"); exists && !force && !bytes.Equal(old, []byte(content)) {
		return nil, fmt.Errorf("%s exists with other content: review it, then pass --force to replace it", path)
	}
	return []byte(content), nil
}

// printCIInit writes what runCIInit did as text.
func printCIInit(cmd *cobra.Command, res *ciInitResult) {
	w := cmd.OutOrStdout()
	dry, _ := cmd.Flags().GetBool("dry-run")
	if !res.Written && !dry {
		_, _ = fmt.Fprintf(w, "%s is up to date\n", res.Path)
		return
	}
	if res.Platform == "gitlab" {
		// The rest of the file is the user's own: show only the entry.
		_, entry := ci.Caller("gitlab")
		verb := map[bool]string{false: "added", true: "would add"}[dry]
		_, _ = fmt.Fprintf(w, "%s the pacto-actions include entry to %s (%d lines):\n%s", verb, res.Path, res.Lines, entry)
		return
	}
	verb := map[bool]string{false: "wrote", true: "would write"}[dry]
	_, _ = fmt.Fprintf(w, "%s %s (%d lines):\n%s", verb, res.Path, res.Lines, res.Content)
	for _, n := range res.Notes {
		_, _ = fmt.Fprintln(w, n)
	}
}
