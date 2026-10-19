package ui

import (
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type promptMode int

const (
	promptCommand promptMode = iota
	promptFilter
)

// Prompt is the ":" command / "/" filter bar.
type Prompt struct {
	*tview.InputField
	app     *App
	mode    promptMode
	active  bool
	prev    string
	suggest string
}

func newPrompt(a *App) *Prompt {
	p := &Prompt{InputField: tview.NewInputField(), app: a}
	p.SetFieldBackgroundColor(tcell.ColorDefault).
		SetFieldTextColor(tcell.ColorWhite).
		SetLabelColor(tcell.ColorAqua)
	p.SetBorder(true).SetBorderPadding(0, 0, 1, 1)
	p.SetChangedFunc(p.changed)
	p.SetDoneFunc(p.done)
	p.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyTab || (ev.Key() == tcell.KeyRight && p.suggest != "") {
			if p.suggest != "" {
				p.SetText(p.suggest)
			}
			return nil
		}
		return ev
	})
	return p
}

func (p *Prompt) activate(mode promptMode) {
	p.mode = mode
	p.active = true
	p.suggest = ""
	p.SetTitle("")
	p.SetText("")
	if mode == promptCommand {
		p.SetLabel("🐶> ").SetBorderColor(tcell.ColorAqua)
	} else {
		p.SetLabel("🐩> ").SetBorderColor(tcell.ColorSeaGreen)
		if f, ok := p.app.top().(Filterable); ok {
			p.prev = f.Filter()
			p.SetText(f.Filter())
		}
	}
	p.app.root.ResizeItem(p, 3, 0)
	p.app.tv.SetFocus(p)
}

func (p *Prompt) deactivate() {
	p.active = false
	p.app.root.ResizeItem(p, 0, 0)
	if t := p.app.top(); t != nil {
		p.app.tv.SetFocus(t)
	}
}

// suggestion returns the first command name extending text.
func suggestion(text string) string {
	if text == "" || strings.Contains(text, " ") {
		return ""
	}
	var out []string
	for _, c := range commandNames() {
		if strings.HasPrefix(c, text) && c != text {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return ""
	}
	sort.Strings(out)
	return out[0]
}

func (p *Prompt) changed(text string) {
	if !p.active {
		return
	}
	if p.mode == promptCommand {
		p.suggest = suggestion(text)
		if p.suggest != "" {
			p.SetTitle(" [gray]tab ⇥ [white]" + p.suggest + " ").SetTitleAlign(tview.AlignRight)
		} else {
			p.SetTitle("")
		}
		return
	}
	if f, ok := p.app.top().(Filterable); ok {
		f.SetFilter(text)
	}
}

func (p *Prompt) done(key tcell.Key) {
	text := strings.TrimSpace(p.GetText())
	switch key {
	case tcell.KeyEnter:
		p.deactivate()
		if p.mode == promptCommand && text != "" {
			if !p.app.runCommand(text) {
				p.app.Flash("err", "unknown command %q (try :help)", text)
			}
		}
		p.app.header.update()
	case tcell.KeyEscape:
		if p.mode == promptFilter {
			if f, ok := p.app.top().(Filterable); ok {
				f.SetFilter(p.prev)
			}
		}
		p.deactivate()
	}
}
