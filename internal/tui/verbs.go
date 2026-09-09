package tui

// Verb is one action that can be triggered on the current selection.
type Verb struct {
	Key   string
	Help  string
	Write bool
}

// verbList returns the verbs available on the current screen.
// filled in by Task 15
var verbList = func(c *Context) []Verb { return nil }
