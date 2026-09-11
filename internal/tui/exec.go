package tui

import (
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
)

// execProcess is a seam so tests can assert on the argv without spawning
// anything. It is restored with t.Cleanup in every test that replaces it.
var execProcess = tea.ExecProcess

// execDoneMsg reports the outcome of a subprocess run.
type execDoneMsg struct{ err error }

// runWrite gates a write verb behind the confirmation screen. The argv shown to
// the reader starts with the literal "pacto" because that is what they would
// have typed; what actually runs is c.Exe with the same arguments, so a pacto
// built somewhere unusual does not silently invoke a different one from PATH.
func runWrite(c *Context, prompt string, argv []string) tea.Cmd {
	if c.ReadOnly {
		// verbList already hides write verbs here. This is the choke point that
		// makes read-only true regardless of what any future caller forgets, so
		// it must not assume the argv it was handed has a shape. Naming the
		// command would also mislead: "lock is not available" is false with
		// lock --check one keystroke away.
		return status("read-only mode: this session runs no writes")
	}
	return push(newConfirmScreen(prompt, argv, func() tea.Cmd {
		return execVerb(c, argv[1:])
	}))
}

// execVerb suspends the TUI and hands the terminal to a real pacto subprocess.
// Writes run this way rather than in-process because the CLI's write paths
// legitimately own the terminal, install signal handlers and, in one case,
// replace the running binary — none of which is safe under an alt screen.
func execVerb(c *Context, args []string) tea.Cmd {
	cmd := exec.Command(c.Exe, args...)
	// The child inherits the environment plus two suppressions. Without the
	// first it re-runs the update check root.go:96 does on every invocation and
	// can print an upgrade banner into the middle of a push. The second keeps
	// its colour out of a frame this process is about to redraw.
	cmd.Env = append(os.Environ(), "PACTO_NO_UPDATE_CHECK=1", "NO_COLOR=1")
	return execProcess(cmd, func(err error) tea.Msg { return execDoneMsg{err: err} })
}
