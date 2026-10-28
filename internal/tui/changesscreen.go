package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/transport"
	"github.com/madushakv/owrt-tui/internal/uci"
)

type phase int

const (
	phaseIdle phase = iota
	phaseApplying
	phasePending // applied with rollback; waiting for the user to confirm
)

var timeouts = []int{30, 60, 120, 300}

type changesScreen struct {
	e        *env
	cur      int
	phase    phase
	userMode *changes.Mode // nil until the user toggles it; then it wins
	tmoIdx   int
	planID   string
	deadline time.Time
	note     string
}

func newChanges(e *env) *changesScreen { return &changesScreen{e: e, tmoIdx: 1} }

func (c *changesScreen) Title() string   { return "Changes" }
func (c *changesScreen) Init() tea.Cmd   { return nil }
func (c *changesScreen) Capturing() bool { return false }
func (c *changesScreen) Keys() string {
	if c.phase == phasePending {
		return "y keep changes  r roll back now  s check status"
	}
	return "a apply  m toggle rollback  t timeout  x clear  backspace unstage"
}

type applyDoneMsg struct {
	plan changes.Plan
	err  error
}
type confirmDoneMsg struct{ err error }
type restoreDoneMsg struct{ err error }
type statusMsg struct {
	id, status string
	err        error
}
type pollMsg string

func (c *changesScreen) mode() changes.Mode {
	if c.userMode != nil {
		return *c.userMode
	}
	return c.e.Staged.RecommendedMode()
}

func (c *changesScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case applyDoneMsg:
		return c.applied(m)
	case confirmDoneMsg:
		if m.err != nil {
			return c, toastErr(fmt.Errorf("confirm failed: %w (the router may still roll back)", m.err))
		}
		c.finish("changes confirmed and kept")
		return c, tea.Batch(toast("changes confirmed and kept"), func() tea.Msg { return uciChangedMsg{} })
	case restoreDoneMsg:
		if m.err != nil {
			return c, toastErr(fmt.Errorf("restore failed: %w", m.err))
		}
		c.phase, c.planID = phaseIdle, ""
		c.note = "restored the previous configuration"
		return c, tea.Batch(toast(c.note), func() tea.Msg { return uciChangedMsg{} })
	case pollMsg:
		if c.phase != phasePending || string(m) != c.planID {
			return c, nil
		}
		return c, c.status()
	case statusMsg:
		if c.phase != phasePending || m.id != c.planID {
			return c, nil
		}
		switch {
		case m.err != nil:
			// Unreachable while the router restarts networking: keep polling.
		case m.status == "rolledback":
			c.phase, c.planID = phaseIdle, ""
			c.note = "the router rolled back to the previous configuration (no confirmation arrived in time)"
			return c, tea.Batch(toastErr(errors.New(c.note)), func() tea.Msg { return uciChangedMsg{} })
		case m.status == "confirmed":
			c.finish("changes confirmed")
			return c, func() tea.Msg { return uciChangedMsg{} }
		}
		return c, c.poll()
	case tea.KeyPressMsg:
		return c.key(m.String())
	}
	return c, nil
}

func (c *changesScreen) finish(note string) {
	c.e.Staged.Clear()
	c.phase, c.planID, c.note, c.cur = phaseIdle, "", note, 0
}

func (c *changesScreen) poll() tea.Cmd {
	id := c.planID
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return pollMsg(id) })
}

func (c *changesScreen) status() tea.Cmd {
	id := c.planID
	return c.e.call(func(ctx context.Context) tea.Msg {
		s, err := c.e.S.RollbackStatus(ctx, id)
		return statusMsg{id, s, err}
	})
}

func (c *changesScreen) applied(m applyDoneMsg) (screen, tea.Cmd) {
	rollback := m.plan.Mode == changes.WithRollback
	switch {
	case m.err == nil && !rollback:
		c.finish("applied")
		return c, tea.Batch(toast("changes applied"), func() tea.Msg { return uciChangedMsg{} })
	case m.err == nil:
		c.phase, c.planID = phasePending, m.plan.ID
		c.deadline = time.Now().Add(time.Duration(m.plan.Timeout) * time.Second)
		c.note = ""
		return c, c.poll()
	case rollback && errors.Is(m.err, transport.ErrConn):
		// Expected when network settings change: the link dropped. The watchdog
		// on the router decides; keep polling so the user can still confirm.
		c.phase, c.planID = phasePending, m.plan.ID
		c.deadline = time.Now().Add(time.Duration(m.plan.Timeout) * time.Second)
		c.note = "connection dropped during apply; if the new settings work, reconnect and press y before the timer ends"
		return c, c.poll()
	}
	c.phase = phaseIdle
	c.note = "apply failed: " + m.err.Error()
	return c, toastErr(m.err)
}

func (c *changesScreen) key(k string) (screen, tea.Cmd) {
	if c.phase == phaseApplying {
		return c, nil
	}
	if c.phase == phasePending {
		id := c.planID
		switch k {
		case "y":
			return c, c.e.call(func(ctx context.Context) tea.Msg {
				return confirmDoneMsg{c.e.S.Confirm(ctx, id)}
			})
		case "r":
			return c, c.e.call(func(ctx context.Context) tea.Msg {
				return restoreDoneMsg{c.e.S.Restore(ctx, id)}
			})
		case "s":
			return c, c.status()
		}
		return c, nil
	}
	n := c.e.Staged.Len()
	switch k {
	case "up", "k":
		c.cur = clampInt(c.cur-1, 0, n-1)
	case "down", "j":
		c.cur = clampInt(c.cur+1, 0, n-1)
	case "backspace", "delete":
		c.e.Staged.Remove(c.cur)
		c.cur = clampInt(c.cur, 0, c.e.Staged.Len()-1)
	case "x":
		if n > 0 {
			return c, func() tea.Msg {
				return askMsg{Prompt: fmt.Sprintf("Discard all %d staged changes?", n), Yes: func() tea.Cmd {
					c.e.Staged.Clear()
					c.cur = 0
					return toast("staged changes discarded")
				}}
			}
		}
	case "m":
		nm := changes.WithRollback
		if c.mode() == changes.WithRollback {
			nm = changes.CommitOnly
		}
		c.userMode = &nm
	case "t":
		c.tmoIdx = (c.tmoIdx + 1) % len(timeouts)
	case "a":
		return c.apply()
	}
	return c, nil
}

func (c *changesScreen) apply() (screen, tea.Cmd) {
	if c.e.Staged.Len() == 0 {
		return c, toast("nothing staged")
	}
	if c.e.S.ReadOnly {
		return c, toast("read-only mode: start with -write to apply changes")
	}
	risk, _ := c.e.Staged.Guard()
	mode := c.mode()
	plan := changes.NewPlan(mode, timeouts[c.tmoIdx])
	prompt := fmt.Sprintf("Apply %d changes with rollback (%ds)?", c.e.Staged.Len(), plan.Timeout)
	if mode == changes.CommitOnly {
		prompt = fmt.Sprintf("Apply %d changes WITHOUT rollback?", c.e.Staged.Len())
	}
	danger := mode == changes.CommitOnly && risk >= changes.RiskMedium
	return c, func() tea.Msg {
		return askMsg{Prompt: prompt, Danger: danger, Yes: func() tea.Cmd {
			c.phase = phaseApplying
			return c.e.callFor(2*time.Minute, func(ctx context.Context) tea.Msg {
				_, err := c.e.S.Apply(ctx, c.e.Staged, plan)
				return applyDoneMsg{plan, err}
			})
		}}
	}
}

func (c *changesScreen) View(w, h int) string {
	var b strings.Builder
	switch c.phase {
	case phaseApplying:
		return "\n  applying..."
	case phasePending:
		left := time.Until(c.deadline).Round(time.Second)
		if left < 0 {
			left = 0
		}
		b.WriteString("\n  " + sWarn.Render(fmt.Sprintf("Applied. Rolling back in %s unless you confirm.", left)) + "\n")
		b.WriteString("  Check that the router and your connection still work, then press y to keep the changes.\n")
		if c.note != "" {
			b.WriteString("\n  " + sDim.Render(c.note) + "\n")
		}
		return b.String()
	}
	items := c.e.Staged.Items()
	if len(items) == 0 {
		msg := "\n  Nothing staged. Edit values on the DHCP or Config screens (2, 3); they appear here for review."
		if c.note != "" {
			msg += "\n\n  " + sDim.Render(c.note)
		}
		return msg
	}
	risk, findings := c.e.Staged.Guard()
	b.WriteString(fmt.Sprintf(" %d staged changes   risk: %s\n", len(items), riskStyle(risk).Render(risk.String())))
	lo, hi := window(len(items), c.cur, h-6-len(findings))
	for i := lo; i < hi; i++ {
		line := "  " + describe(items[i])
		if i == c.cur {
			line = sSel.Render(clip(line, w))
		}
		b.WriteString(line + "\n")
	}
	for _, f := range findings {
		b.WriteString(riskStyle(f.Risk).Render("  ! "+f.Why) + "\n")
	}
	mode := "commit only (no safety net)"
	if c.mode() == changes.WithRollback {
		mode = fmt.Sprintf("with rollback, %ds to confirm", timeouts[c.tmoIdx])
	}
	b.WriteString("\n  apply mode: " + sBold.Render(mode) + "\n")
	return b.String()
}

// describe is Change.Describe with secret values masked, so a staged password
// or key never appears on screen.
func describe(c changes.Change) string {
	if c.Option != "" && uci.IsSecret(c.Option) {
		if c.Old != "" {
			c.Old = uci.MaskText
		}
		if c.Value != "" {
			c.Value = uci.MaskText
		}
	}
	return c.Describe()
}

func riskStyle(r changes.Risk) lipgloss.Style {
	switch {
	case r >= changes.RiskHigh:
		return sBad
	case r >= changes.RiskLow:
		return sWarn
	}
	return sGood
}
