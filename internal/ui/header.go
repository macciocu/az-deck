package ui

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"
)

var logo = []string{"azd (az-deck)"}

const menuRows = 6

// Header shows cluster-style info, the key menu and the logo, like k9s.
type Header struct {
	*tview.Flex
	app  *App
	info *tview.TextView
	menu *tview.TextView
	logo *tview.TextView
}

func newHeader(a *App) *Header {
	h := &Header{
		Flex: tview.NewFlex(),
		app:  a,
		info: tview.NewTextView().SetDynamicColors(true),
		menu: tview.NewTextView().SetDynamicColors(true),
		logo: tview.NewTextView().SetDynamicColors(true),
	}
	h.AddItem(h.info, 48, 0, false).AddItem(h.menu, 0, 1, false).AddItem(h.logo, 12, 0, false)
	var b strings.Builder
	for _, l := range logo {
		fmt.Fprintf(&b, "[%s::b]%s\n", colorLogo, tview.Escape(l))
	}
	h.logo.SetText(b.String())
	return h
}

func (h *Header) update() {
	a := h.app
	sub := a.az.Subscription()
	scope := a.Scope()
	rg, env := scope.RG, scope.Env
	if rg == "" {
		rg = "all"
	}
	if env == "" {
		env = "all"
	}
	mode := ""
	if a.opts.ReadOnly {
		mode = " [orangered::b](read-only)"
	}
	rows := [][2]string{
		{"Subscription", sub.Name},
		{"Sub ID", sub.ID},
		{"Tenant", sub.TenantID},
		{"Res Group", rg},
		{"Environment", env},
		{"azd Rev", a.opts.Version},
	}
	var b strings.Builder
	for i, r := range rows {
		fmt.Fprintf(&b, "[%s::b]%-14s[%s::-]%s", colorInfoKey, r[0]+":", colorInfoVal, tview.Escape(trunc(r[1], 33)))
		if i == 5 {
			b.WriteString(mode)
		}
		b.WriteByte('\n')
	}
	h.info.SetText(b.String())

	var hints []Hint
	if t := a.top(); t != nil {
		hints = t.Hints()
	}
	h.menu.SetText(renderMenu(hints))
}

// renderMenu lays hints out column by column, menuRows per column.
func renderMenu(hints []Hint) string {
	if len(hints) == 0 {
		return ""
	}
	cols := (len(hints) + menuRows - 1) / menuRows
	widths := make([]int, cols)
	for i, h := range hints {
		c := i / menuRows
		widths[c] = max(widths[c], len(h.Key)+len(h.Desc)+3)
	}
	lines := make([]strings.Builder, menuRows)
	for i, h := range hints {
		c, r := i/menuRows, i%menuRows
		color := colorMenuKey
		if h.Num {
			color = colorMenuNum
		}
		cell := fmt.Sprintf("[%s::b]<%s> [%s::-]%s", color, tview.Escape(h.Key), colorMenuDesc, tview.Escape(h.Desc))
		pad := widths[c] - (len(h.Key) + len(h.Desc) + 3) + 2
		lines[r].WriteString(cell + strings.Repeat(" ", pad))
	}
	var b strings.Builder
	for i := range lines {
		b.WriteString(lines[i].String())
		b.WriteByte('\n')
	}
	return b.String()
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
