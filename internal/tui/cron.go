package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/cron"
	"github.com/madushakv/owrt-tui/internal/health"
)

type cronScreen struct {
	e       *env
	text    string
	jobs    []cron.Job
	cur     int
	loading bool
	err     error
	form    *form
}

func newCron(e *env) *cronScreen { return &cronScreen{e: e, loading: true} }

func (c *cronScreen) Title() string   { return "Cron" }
func (c *cronScreen) Capturing() bool { return c.form != nil }
func (c *cronScreen) Keys() string {
	if c.form != nil {
		return "enter edit  s save  esc cancel"
	}
	return "t switch off/on  d delete  a add a job  g reload"
}

type cronMsg struct {
	text string
	err  error
}

func (c *cronScreen) Init() tea.Cmd { return c.load() }

func (c *cronScreen) load() tea.Cmd {
	return c.e.call(func(ctx context.Context) tea.Msg {
		t, err := c.e.S.Crontab(ctx)
		return cronMsg{t, err}
	})
}

func (c *cronScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case cronMsg:
		c.loading, c.err = false, m.err
		if m.err == nil {
			c.text, c.jobs = m.text, cron.Parse(m.text)
			c.cur = clampInt(c.cur, 0, len(c.jobs)-1)
		}
	case cronSavedMsg:
		return c, tea.Batch(toast(m.what), c.load())
	case tea.KeyPressMsg:
		if c.form != nil {
			return c.formKey(m)
		}
		return c.key(m.String())
	}
	return c, nil
}

func (c *cronScreen) readOnly() tea.Cmd {
	if c.e.S.ReadOnly {
		return toast("read-only mode: start with -write to change the schedule")
	}
	return nil
}

func (c *cronScreen) key(k string) (screen, tea.Cmd) {
	switch k {
	case "up", "k":
		c.cur = clampInt(c.cur-1, 0, len(c.jobs)-1)
	case "down", "j":
		c.cur = clampInt(c.cur+1, 0, len(c.jobs)-1)
	case "g":
		c.loading = true
		return c, c.load()
	case "t", "d":
		if t := c.readOnly(); t != nil {
			return c, t
		}
		if len(c.jobs) == 0 {
			return c, nil
		}
		j := c.jobs[c.cur]
		if k == "t" {
			nt, err := cron.Toggle(c.text, j.Line)
			if err != nil {
				return c, toastErr(err)
			}
			return c, c.e.writeCron(nt, "job switched")
		}
		return c, func() tea.Msg {
			return askMsg{Prompt: fmt.Sprintf("Delete the job '%s %s'?", j.Schedule, clip(j.Command, 50)), Danger: true, Yes: func() tea.Cmd {
				nt, err := cron.Remove(c.text, j.Line)
				if err != nil {
					return toastErr(err)
				}
				return c.e.writeCron(nt, "job deleted")
			}}
		}
	case "a":
		if t := c.readOnly(); t != nil {
			return c, t
		}
		c.form = newForm("Add a cron job",
			&field{Label: "Schedule", Key: "sched", Required: true, Hint: "minute hour day month weekday, e.g. 0 3 * * 1  (Mondays 03:00)", Validate: cron.ValidSchedule},
			&field{Label: "Command", Key: "cmd", Required: true, Validate: cron.ValidCommand, Hint: "one line; log to /tmp, not flash, if it runs often"},
		)
	}
	return c, nil
}

func (c *cronScreen) formKey(m tea.KeyPressMsg) (screen, tea.Cmd) {
	cmd := c.form.Update(m)
	switch {
	case c.form.cancelled:
		c.form = nil
	case c.form.done:
		f := c.form
		c.form = nil
		sched, command := val(f, "sched"), val(f, "cmd")
		nt, err := cron.Add(c.text, sched, command, "")
		if err != nil {
			return c, toastErr(err)
		}
		prompt := fmt.Sprintf("Add '%s %s'?", sched, clip(command, 50))
		if p := cron.FlashWrite(command); p != "" && cron.Frequent(sched) {
			prompt += " Warning: it runs often and writes to " + p + " (flash wears out); log to /tmp instead."
		}
		return c, func() tea.Msg {
			return askMsg{Prompt: prompt, Danger: false, Yes: func() tea.Cmd { return c.e.writeCron(nt, "job added") }}
		}
	}
	return c, cmd
}

func (c *cronScreen) View(w, h int) string {
	if c.form != nil {
		return c.form.View(w, h)
	}
	if c.loading && len(c.jobs) == 0 {
		return "\n  loading..."
	}
	if c.err != nil {
		return "\n  " + sBad.Render("error: "+c.err.Error())
	}
	if len(c.jobs) == 0 {
		return "\n  no cron jobs. Press a to add one, or use Tasks for a schedule or scheduled reboot."
	}
	var b strings.Builder
	b.WriteString(sDim.Render(fmt.Sprintf("  %-4s %-16s %s", "", "SCHEDULE", "COMMAND")) + "\n")
	lo, hi := window(len(c.jobs), c.cur, h-2)
	for i := lo; i < hi; i++ {
		j := c.jobs[i]
		state := "on "
		if j.Off {
			state = "off"
		}
		cmd := health.MaskCron(j.Command)
		note := ""
		if p := cron.FlashWrite(j.Command); p != "" && cron.Frequent(j.Schedule) && !j.Off {
			note = "  ! writes to flash: " + p
		}
		if j.Tag != "" {
			note += "  [owrt-tui]"
		}
		line := fmt.Sprintf("  %-4s %-16s %s%s", state, j.Schedule, cmd, note)
		if i == c.cur {
			line = sSel.Render(clip(line, w))
		} else {
			line = clip(line, w)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
