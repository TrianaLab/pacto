package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// outputMaxLines bounds the output pane. A verb that prints without limit must
// not be able to exhaust memory just because nobody scrolled away.
//
// ponytail: fixed line cap, not a byte budget. Switch to bytes if one verb ever
// emits lines long enough to matter on their own.
const outputMaxLines = 5000

// nextOutputID hands out run identifiers. Messages carry the id of the run that
// produced them so a superseded verb's late output is discarded instead of
// bleeding into the next one's pane.
var nextOutputID int

// outputLineMsg is one line of verb output.
type outputLineMsg struct {
	id   int
	line string
}

// outputDoneMsg ends a run.
type outputDoneMsg struct {
	id  int
	err error
}

// outputScreen shows one verb's output.
type outputScreen struct {
	id      int
	title   string
	lines   []string
	dropped int
	deps    int
	running bool
	err     error
	done    bool
	sp      spinner.Model
	vp      viewport.Model
}

func newOutputScreen(title string) *outputScreen {
	nextOutputID++
	return &outputScreen{
		id:      nextOutputID,
		title:   title,
		running: true,
		sp:      spinner.New(spinner.WithSpinner(spinner.Dot)),
		vp:      viewport.New(),
	}
}

// append adds a line, dropping the oldest when the buffer is full.
//
// This is the only door into the pane's buffer, so it is where the line is
// cleaned. safeFragment rather than safeText because a line arrives already
// part-rendered: emit splits the output of renderValidate and its siblings,
// which style their verdicts and hand pkg/graph's coloured trees through whole.
func (o *outputScreen) append(line string) {
	if len(o.lines) >= outputMaxLines {
		drop := len(o.lines) - outputMaxLines + 1
		o.lines = o.lines[drop:]
		o.dropped += drop
	}
	o.lines = append(o.lines, safeFragment(line))
}

func (o *outputScreen) Title() string { return o.title }

func (o *outputScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case outputLineMsg:
		if msg.id == o.id {
			o.append(msg.line)
		}
		return o, nil
	case outputDoneMsg:
		if msg.id == o.id {
			o.running, o.done, o.err = false, true, msg.err
		}
		return o, nil
	case depResolvedMsg:
		if msg.id == o.id {
			o.deps++
		}
		return o, nil
	case spinner.TickMsg:
		sp, cmd := o.sp.Update(msg)
		o.sp = sp
		return o, cmd
	}
	o.resize(c)
	vp, cmd := o.vp.Update(msg)
	o.vp = vp
	return o, cmd
}

func (o *outputScreen) resize(c *Context) {
	o.vp.SetWidth(c.Width)
	h := c.Height - 4
	if h < 3 {
		h = 3
	}
	o.vp.SetHeight(h)
	o.vp.SetContent(o.content())
}

func (o *outputScreen) content() string {
	var b strings.Builder
	if o.dropped > 0 {
		b.WriteString(warnStyle.Render(fmt.Sprintf("... %d earlier lines dropped", o.dropped)) + "\n")
	}
	b.WriteString(strings.Join(o.lines, "\n"))
	return b.String()
}

func (o *outputScreen) View(c *Context) string {
	o.resize(c)
	return o.status() + "\n" + o.vp.View()
}

func (o *outputScreen) status() string {
	switch {
	case o.err != nil:
		return errorStyle.Render("failed: " + o.err.Error())
	case o.done:
		return okStyle.Render("done")
	default:
		s := o.sp.View() + " running"
		if o.deps > 0 {
			s += fmt.Sprintf("  %d dependencies resolved", o.deps)
		}
		return dimStyle.Render(s)
	}
}
