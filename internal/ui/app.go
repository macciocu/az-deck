// Package ui implements the k9s-style terminal interface.
package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/macciocu/az-deck/internal/azure"
	"github.com/macciocu/az-deck/internal/config"
)

// Hint is a key binding shown in the header menu.
type Hint struct {
	Key, Desc string
	Num       bool
}

// Component is a view that lives on the navigation stack.
type Component interface {
	tview.Primitive
	Crumb() string
	Hints() []Hint
	Start()
	Stop()
}

// Filterable components accept the "/" filter.
type Filterable interface {
	SetFilter(string)
	Filter() string
}

// Refresher components can be refreshed on demand.
type Refresher interface {
	Refresh()
}

// Options configure the application.
type Options struct {
	Version       string
	Subscription  string
	ResourceGroup string
	Command       string
	Refresh       time.Duration
	Shell         string
	ReadOnly      bool
}

// Scope is the active resource group / environment filter.
type Scope struct {
	RG, Env string
}

// App is the root of the TUI.
type App struct {
	tv    *tview.Application
	az    *azure.Client
	state *config.State
	opts  Options

	root    *tview.Flex
	header  *Header
	prompt  *Prompt
	pages   *tview.Pages
	crumbs  *tview.TextView
	flash   *tview.TextView
	stack   []Component
	dialogs int

	mu       sync.Mutex
	scope    Scope
	flashGen int
}

// New builds the application. The Azure client must already point at a subscription.
func New(az *azure.Client, state *config.State, opts Options) *App {
	initStyles()
	if opts.Refresh <= 0 {
		opts.Refresh = 5 * time.Second
	}
	if opts.Shell == "" {
		opts.Shell = "sh"
	}
	a := &App{tv: tview.NewApplication(), az: az, state: state, opts: opts}
	a.scope.RG = opts.ResourceGroup

	a.header = newHeader(a)
	a.prompt = newPrompt(a)
	a.pages = tview.NewPages()
	a.crumbs = tview.NewTextView().SetDynamicColors(true)
	a.flash = tview.NewTextView().SetDynamicColors(true)

	a.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 7, 0, false).
		AddItem(a.prompt, 0, 0, false).
		AddItem(a.pages, 0, 1, true).
		AddItem(a.crumbs, 1, 0, false).
		AddItem(a.flash, 1, 0, false)

	a.tv.SetRoot(a.root, true).EnableMouse(false)
	a.tv.SetInputCapture(a.keyboard)
	return a
}

// Run starts the event loop.
func (a *App) Run() error {
	cmd := a.opts.Command
	if cmd == "" {
		cmd = "apps"
	}
	if !a.runCommand(cmd) {
		a.runCommand("apps")
	}
	defer func() {
		if len(a.stack) > 0 {
			a.stack[len(a.stack)-1].Stop()
		}
	}()
	return a.tv.Run()
}

// Scope returns the active scope.
func (a *App) Scope() Scope {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.scope
}

// SetScope changes the active resource group / environment scope.
func (a *App) SetScope(s Scope) {
	a.mu.Lock()
	a.scope = s
	a.mu.Unlock()
	if s.RG != "" {
		a.state.TouchRG(s.RG)
	}
	a.state.RG = s.RG
	_ = a.state.Save()
	a.header.update()
}

// QueueUpdateDraw schedules f on the UI goroutine.
func (a *App) QueueUpdateDraw(f func()) { a.tv.QueueUpdateDraw(f) }

func (a *App) top() Component {
	if len(a.stack) == 0 {
		return nil
	}
	return a.stack[len(a.stack)-1]
}

// Push adds a component on top of the stack.
func (a *App) Push(c Component) {
	if t := a.top(); t != nil {
		t.Stop()
	}
	a.stack = append(a.stack, c)
	a.pages.AddAndSwitchToPage(pageName(len(a.stack)), c, true)
	a.tv.SetFocus(c)
	c.Start()
	a.refreshChrome()
}

// Reset replaces the whole stack with a single component.
func (a *App) Reset(c Component) {
	if t := a.top(); t != nil {
		t.Stop()
	}
	for i := range a.stack {
		a.pages.RemovePage(pageName(i + 1))
	}
	a.stack = nil
	a.Push(c)
}

// Pop removes the top component, returning to the previous view.
func (a *App) Pop() {
	if len(a.stack) <= 1 {
		return
	}
	t := a.top()
	t.Stop()
	a.pages.RemovePage(pageName(len(a.stack)))
	a.stack = a.stack[:len(a.stack)-1]
	n := a.top()
	a.pages.SwitchToPage(pageName(len(a.stack)))
	a.tv.SetFocus(n)
	n.Start()
	a.refreshChrome()
}

func pageName(i int) string { return fmt.Sprintf("view-%d", i) }

func (a *App) refreshChrome() {
	a.header.update()
	var b strings.Builder
	for i, c := range a.stack {
		bg := "aqua"
		if i == len(a.stack)-1 {
			bg = "orange"
		}
		fmt.Fprintf(&b, "[black:%s:b] <%s> [-:-:-] ", bg, tview.Escape(strings.ToLower(c.Crumb())))
	}
	a.crumbs.SetText(b.String())
}

// Flash shows a transient status message.
func (a *App) Flash(level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	color := "navajowhite"
	icon := "😎"
	switch level {
	case "warn":
		color, icon = "orange", "😗"
	case "err":
		color, icon = "orangered", "😡"
	}
	a.flashGen++
	gen := a.flashGen
	a.flash.SetText(fmt.Sprintf("[%s::b]%s %s", color, icon, tview.Escape(msg)))
	time.AfterFunc(6*time.Second, func() {
		a.tv.QueueUpdateDraw(func() {
			if a.flashGen == gen {
				a.flash.Clear()
			}
		})
	})
}

// FlashAsync is Flash usable from any goroutine.
func (a *App) FlashAsync(level, format string, args ...any) {
	a.tv.QueueUpdateDraw(func() { a.Flash(level, format, args...) })
}

// Async runs a long operation off the UI goroutine, flashing progress and
// result, then refreshes the current view.
func (a *App) Async(what string, fn func(ctx context.Context) error) {
	a.Flash("info", "%s…", what)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		err := fn(ctx)
		a.tv.QueueUpdateDraw(func() {
			if err != nil {
				a.Flash("err", "%s failed: %s", what, azure.ErrorMessage(err))
				return
			}
			a.Flash("info", "%s: done", what)
			if r, ok := a.top().(Refresher); ok {
				r.Refresh()
			}
		})
	}()
}

// guardWrite blocks mutating actions in read-only mode.
func (a *App) guardWrite() bool {
	if a.opts.ReadOnly {
		a.Flash("warn", "read-only mode: action disabled")
		return false
	}
	return true
}

func (a *App) keyboard(ev *tcell.EventKey) *tcell.EventKey {
	if a.prompt.active || a.dialogs > 0 {
		return ev
	}
	switch ev.Key() {
	case tcell.KeyCtrlC:
		a.tv.Stop()
		return nil
	case tcell.KeyCtrlR:
		if r, ok := a.top().(Refresher); ok {
			r.Refresh()
			a.Flash("info", "refreshed")
		}
		return nil
	case tcell.KeyEscape:
		if f, ok := a.top().(Filterable); ok && f.Filter() != "" {
			f.SetFilter("")
			a.header.update()
			return nil
		}
		a.Pop()
		return nil
	case tcell.KeyRune:
		switch ev.Rune() {
		case ':':
			a.prompt.activate(promptCommand)
			return nil
		case '/':
			if _, ok := a.top().(Filterable); ok {
				a.prompt.activate(promptFilter)
				return nil
			}
		case '?':
			if _, ok := a.top().(*HelpView); !ok {
				a.Push(newHelpView(a, a.top()))
			}
			return nil
		}
	}
	return ev
}

// showDialog places a modal primitive above the current view.
func (a *App) showDialog(name string, p tview.Primitive, width, height int) {
	a.dialogs++
	grid := tview.NewGrid().SetColumns(0, width, 0).SetRows(0, height, 0).AddItem(p, 1, 1, 1, 1, 0, 0, true)
	a.pages.AddPage(name, grid, true, true)
	a.tv.SetFocus(p)
}

func (a *App) closeDialog(name string) {
	a.pages.RemovePage(name)
	if a.dialogs > 0 {
		a.dialogs--
	}
	if t := a.top(); t != nil {
		a.tv.SetFocus(t)
	}
}

// Confirm asks a yes/no question.
func (a *App) Confirm(title, msg string, ok func()) {
	m := tview.NewModal().
		SetText(msg).
		AddButtons([]string{"Cancel", "OK"}).
		SetBackgroundColor(tcell.ColorDefault).
		SetTextColor(tcell.ColorWhite).
		SetButtonBackgroundColor(tcell.ColorDodgerBlue).
		SetButtonTextColor(tcell.ColorBlack)
	m.SetBorderColor(colorError).SetTitle(" <" + title + "> ").SetTitleColor(colorError)
	m.SetDoneFunc(func(_ int, label string) {
		a.pages.RemovePage("confirm")
		if a.dialogs > 0 {
			a.dialogs--
		}
		a.tv.SetFocus(a.top())
		if label == "OK" {
			ok()
		}
	})
	a.dialogs++
	a.pages.AddPage("confirm", m, true, true)
	a.tv.SetFocus(m)
}

// Pick asks the user to choose one of several options.
func (a *App) Pick(title string, options []string, chosen func(string)) {
	l := tview.NewList().ShowSecondaryText(false).
		SetMainTextColor(colorTableFg).
		SetSelectedBackgroundColor(colorCursorBg).
		SetSelectedTextColor(colorCursorFg)
	l.SetBorder(true).SetTitle(" " + title + " ").SetTitleColor(tcell.ColorAqua)
	for i, o := range options {
		o := o
		var sc rune
		if i < 9 {
			sc = rune('1' + i)
		}
		l.AddItem(o, "", sc, func() {
			a.closeDialog("pick")
			chosen(o)
		})
	}
	l.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch {
		case ev.Key() == tcell.KeyEscape:
			a.closeDialog("pick")
			return nil
		case ev.Rune() == 'j':
			return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
		case ev.Rune() == 'k':
			return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
		}
		return ev
	})
	w := 30
	for _, o := range options {
		w = max(w, len(o)+10)
	}
	a.showDialog("pick", l, w, len(options)+2)
}
