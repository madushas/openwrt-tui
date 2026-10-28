package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/validate"
)

type fieldKind int

const (
	kText   fieldKind = iota
	kBool             // Value is "1" or "" (an unset option means off)
	kChoice           // Value is one of Choices
)

// field is one editable UCI option.
type field struct {
	Label    string
	Key      string // UCI option name
	Kind     fieldKind
	Choices  []string
	Value    string
	Orig     string
	Required bool
	Validate validate.Func
	Hint     string
	// Secret fields are never loaded with the current value and never shown:
	// an empty Value means "leave as it is".
	Secret bool
	err    string
}

// form is a small vertical form whose fields are bound to UCI options. It
// validates as the user types and only produces staged changes; it never talks
// to the router.
type form struct {
	Title   string
	fields  []*field
	cur     int
	in      textinput.Model
	editing bool
	// Cross checks rules that involve several fields; it sets field errors.
	Cross func(f *form)
	// done is set when the user saved; cancelled when they left without saving.
	done, cancelled bool
}

func newForm(title string, fields ...*field) *form {
	in := textinput.New()
	in.Prompt = ""
	for _, f := range fields {
		f.Orig = f.Value
	}
	return &form{Title: title, fields: fields, in: in}
}

// Capturing is true while a text field has focus.
func (f *form) Capturing() bool { return f.editing }

// check validates one field and records its message.
func (fl *field) check() bool {
	fl.err = ""
	switch {
	case fl.Value == "":
		if fl.Required {
			fl.err = "required"
		}
	case fl.Validate != nil:
		if err := fl.Validate(fl.Value); err != nil {
			fl.err = err.Error()
		}
	}
	return fl.err == ""
}

// valid validates every field, so the user sees all problems at once.
func (f *form) valid() bool {
	ok := true
	for _, fl := range f.fields {
		if !fl.check() {
			ok = false
		}
	}
	if f.Cross != nil {
		f.Cross(f)
		for _, fl := range f.fields {
			if fl.err != "" {
				ok = false
			}
		}
	}
	return ok
}

// dirty reports whether anything differs from the loaded values.
func (f *form) dirty() bool {
	for _, fl := range f.fields {
		if fl.Value != fl.Orig {
			return true
		}
	}
	return false
}

// Changes turns edited fields into staged changes for one section. An emptied
// field deletes the option.
func (f *form) Changes(cfg, sec string) []changes.Change {
	var out []changes.Change
	for _, fl := range f.fields {
		if fl.Value == fl.Orig || (fl.Secret && fl.Value == "") {
			continue
		}
		if fl.Secret { // the previous value is never put into the diff
			out = append(out, changes.Change{Op: changes.OpSet, Config: cfg, Section: sec, Option: fl.Key, Value: fl.Value})
			continue
		}
		if fl.Value == "" {
			out = append(out, changes.Change{Op: changes.OpDelete, Config: cfg, Section: sec, Option: fl.Key, Old: fl.Orig})
			continue
		}
		out = append(out, changes.Change{Op: changes.OpSet, Config: cfg, Section: sec, Option: fl.Key, Value: fl.Value, Old: fl.Orig})
	}
	return out
}

// Update handles one key. It reports through done/cancelled.
func (f *form) Update(m tea.KeyPressMsg) tea.Cmd {
	fl := f.fields[f.cur]
	if f.editing {
		switch m.String() {
		case "esc":
			f.editing = false
			f.in.Blur()
		case "enter":
			fl.Value = strings.TrimSpace(f.in.Value())
			fl.check()
			f.editing = false
			f.in.Blur()
			if f.cur < len(f.fields)-1 {
				f.cur++
			}
		default:
			var cmd tea.Cmd
			f.in, cmd = f.in.Update(m)
			fl.Value = f.in.Value() // validate as the user types
			fl.check()
			return cmd
		}
		return nil
	}
	switch m.String() {
	case "up", "k", "shift+tab":
		f.cur = clampInt(f.cur-1, 0, len(f.fields)-1)
	case "down", "j", "tab":
		f.cur = clampInt(f.cur+1, 0, len(f.fields)-1)
	case "esc":
		f.cancelled = true
	case "s", "ctrl+s":
		if f.valid() {
			f.done = true
		}
	case "enter", " ", "e":
		switch fl.Kind {
		case kBool:
			if fl.Value == "" {
				fl.Value = "1"
			} else {
				fl.Value = ""
			}
		case kChoice:
			i := 0
			for j, c := range fl.Choices {
				if c == fl.Value {
					i = j + 1
				}
			}
			fl.Value = fl.Choices[i%len(fl.Choices)]
		default:
			f.editing = true
			f.in.SetValue(fl.Value)
			f.in.EchoMode = textinput.EchoNormal
			if fl.Secret {
				f.in.EchoMode = textinput.EchoPassword
			}
			return f.in.Focus()
		}
	}
	return nil
}

// View draws the form in w x h.
func (f *form) View(w, h int) string {
	var b strings.Builder
	b.WriteString("  " + sBold.Render(f.Title) + "\n\n")
	lw := 0
	for _, fl := range f.fields {
		if n := len(fl.Label); n > lw {
			lw = n
		}
	}
	for i, fl := range f.fields {
		val := fl.Value
		switch fl.Kind {
		case kBool:
			val = "[ ]"
			if fl.Value != "" {
				val = "[x]"
			}
		case kText:
			switch {
			case fl.Secret && val != "":
				val = strings.Repeat("•", len([]rune(val)))
			case fl.Secret:
				val = sDim.Render("(unchanged)")
			case val == "":
				val = sDim.Render("(not set)")
			}
		}
		if f.editing && i == f.cur {
			val = f.in.View() + "▏"
		}
		mark := "  "
		lab := fmt.Sprintf("%-*s", lw, fl.Label)
		if i == f.cur {
			mark, lab = sAccent.Render("▸ "), sAccent.Render(lab)
		}
		line := mark + lab + "  " + val
		if fl.Value != fl.Orig {
			line += " " + sWarn.Render("●")
		}
		if fl.err != "" {
			line += "  " + sBad.Render(fl.err)
		}
		b.WriteString(line + "\n")
		if i == f.cur && fl.Hint != "" {
			b.WriteString("  " + strings.Repeat(" ", lw+2) + sDim.Render(fl.Hint) + "\n")
		}
	}
	b.WriteString("\n  " + sDim.Render("enter edit/toggle   s stage changes   esc cancel   ● = changed") + "\n")
	return fitLines(b.String(), w, h)
}
