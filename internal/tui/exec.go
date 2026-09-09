package tui

import (
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
		// makes read-only true regardless of what any future caller forgets.
		return status("read-only mode: " + argv[1] + " is not available")
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
	return execProcess(cmd, func(err error) tea.Msg { return execDoneMsg{err: err} })
}
