package ui

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"
)

// HelpView lists key bindings for the previous view plus global ones.
type HelpView struct {
	*tview.TextView
}

func newHelpView(a *App, prev Component) *HelpView {
	v := &HelpView{TextView: tview.NewTextView().SetDynamicColors(true).SetScrollable(true)}
	v.SetBorder(true).SetBorderPadding(1, 0, 2, 2).SetBorderColor(colorBorderFoc)
	v.SetTitle(fmt.Sprintf(" [%s::b]Help ", colorTitle))

	var res []Hint
	if prev != nil {
		res = prev.Hints()
	}
	sections := []struct {
		title string
		hints []Hint
	}{
		{"RESOURCE", res},
		{"GENERAL", []Hint{
			{Key: ":cmd", Desc: "Command mode"},
			{Key: "/term", Desc: "Filter (regex, !inverse)"},
			{Key: "esc", Desc: "Back / clear filter"},
			{Key: "ctrl-r", Desc: "Refresh"},
			{Key: "?", Desc: "Help"},
			{Key: "ctrl-c", Desc: "Quit"},
			{Key: ":q", Desc: "Quit"},
		}},
		{"NAVIGATION", []Hint{
			{Key: "j / k", Desc: "Down / Up"},
			{Key: "h / l", Desc: "Left / Right"},
			{Key: "g / G", Desc: "Top / Bottom"},
			{Key: "ctrl-f", Desc: "Page down"},
			{Key: "ctrl-b", Desc: "Page up"},
			{Key: "enter", Desc: "Drill down"},
		}},
		{"COMMANDS", commandHints()},
	}
	var b strings.Builder
	for _, s := range sections {
		fmt.Fprintf(&b, "[%s::b]%s[-::-]\n", colorInfoKey, s.title)
		for _, h := range s.hints {
			fmt.Fprintf(&b, "  [%s::b]%-22s[%s::-]%s\n", colorMenuKey, tview.Escape("<"+h.Key+">"), colorMenuDesc, tview.Escape(h.Desc))
		}
		b.WriteByte('\n')
	}
	v.SetText(b.String())
	return v
}

// Crumb implements Component.
func (v *HelpView) Crumb() string { return "help" }

// Hints implements Component.
func (v *HelpView) Hints() []Hint { return []Hint{{Key: "esc", Desc: "Back"}} }

// Start implements Component.
func (v *HelpView) Start() {}

// Stop implements Component.
func (v *HelpView) Stop() {}
