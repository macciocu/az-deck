package ui

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/macciocu/az-deck/internal/azure"
)

// ColKind controls how a column is rendered and sorted.
type ColKind int

const (
	ColText ColKind = iota
	ColNum
	ColAge // cell holds an RFC3339 timestamp
)

// Column describes a table column.
type Column struct {
	Name string
	Kind ColKind
}

// Row is one resource in a table.
type Row struct {
	ID     string
	Cells  []string
	Status string // drives the row color
	Data   any
}

// Action is a resource-specific key binding.
type Action struct {
	Key   tcell.Key // tcell.KeyRune for Rune bindings
	Rune  rune
	Label string
	Write bool // mutating action (blocked in read-only mode)
	Fn    func(Row)
}

func (ac Action) keyName() string {
	if ac.Key == tcell.KeyRune {
		return string(ac.Rune)
	}
	return strings.ToLower(strings.ReplaceAll(tcell.KeyNames[ac.Key], "Ctrl-", "ctrl-"))
}

// Resource defines how a resource type is listed and operated on.
type Resource struct {
	Title    string // e.g. "ContainerApps"
	Scope    string // shown in parentheses next to the title
	Columns  []Column
	List     func(ctx context.Context) ([]Row, error)
	Enter    func(Row)
	EnterTip string
	Describe func(ctx context.Context, r Row) (string, error)
	YAML     func(ctx context.Context, r Row) (any, error)
	Actions  []Action
	RGKeys   bool // number keys switch resource-group scope
	SortCol  int
	SortDesc bool
}

// TableView renders a Resource as a k9s-style table.
type TableView struct {
	*tview.Table
	app *App
	res *Resource

	rows     []Row
	visible  []Row
	filter   string
	sortCol  int
	sortAsc  bool
	err      error
	loading  atomic.Bool
	loadedAt time.Time
	cancel   context.CancelFunc
}

func newTableView(a *App, res *Resource) *TableView {
	v := &TableView{Table: tview.NewTable(), app: a, res: res, sortCol: res.SortCol, sortAsc: !res.SortDesc}
	v.SetFixed(1, 0).SetSelectable(true, false).SetBorder(true).SetBorderPadding(0, 0, 1, 1)
	v.SetSelectedStyle(tcell.StyleDefault.Foreground(colorCursorFg).Background(colorCursorBg).Bold(true))
	v.SetBorderColor(colorBorderFoc)
	v.SetTitleAlign(tview.AlignCenter)
	v.SetInputCapture(v.keyboard)
	v.SetSelectedFunc(func(row, _ int) {
		if r, ok := v.selected(); ok && v.res.Enter != nil {
			v.res.Enter(r)
		}
	})
	v.setTitle()
	return v
}

// Crumb implements Component.
func (v *TableView) Crumb() string { return v.res.Title }

// Start begins periodic refresh.
func (v *TableView) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	v.Refresh()
	go func() {
		t := time.NewTicker(v.app.opts.Refresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				v.Refresh()
			}
		}
	}()
}

// Stop halts refreshing.
func (v *TableView) Stop() {
	if v.cancel != nil {
		v.cancel()
	}
}

// Refresh reloads the rows in the background.
func (v *TableView) Refresh() {
	if !v.loading.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer v.loading.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		rows, err := v.res.List(ctx)
		v.app.QueueUpdateDraw(func() {
			if err != nil {
				v.err = err
				v.app.Flash("err", "%s: %s", v.res.Title, azure.ErrorMessage(err))
			} else {
				v.err = nil
				v.rows = rows
				v.loadedAt = time.Now()
			}
			v.render()
		})
	}()
	if v.loadedAt.IsZero() {
		v.render()
	}
}

// Filter implements Filterable.
func (v *TableView) Filter() string { return v.filter }

// SetFilter implements Filterable.
func (v *TableView) SetFilter(f string) {
	v.filter = f
	v.render()
}

// Hints implements Component.
func (v *TableView) Hints() []Hint {
	var h []Hint
	if v.res.RGKeys {
		h = append(h, Hint{Key: "0", Desc: "all", Num: true})
		for i, rg := range v.app.state.RecentRGs {
			if i >= 5 {
				break
			}
			h = append(h, Hint{Key: strconv.Itoa(i + 1), Desc: rg, Num: true})
		}
	}
	if v.res.Enter != nil && v.res.EnterTip != "" {
		h = append(h, Hint{Key: "enter", Desc: v.res.EnterTip})
	}
	if v.res.Describe != nil {
		h = append(h, Hint{Key: "d", Desc: "Describe"})
	}
	if v.res.YAML != nil {
		h = append(h, Hint{Key: "y", Desc: "YAML"})
	}
	for _, ac := range v.res.Actions {
		if ac.Write && v.app.opts.ReadOnly {
			continue
		}
		h = append(h, Hint{Key: ac.keyName(), Desc: ac.Label})
	}
	h = append(h,
		Hint{Key: "c", Desc: "Copy"},
		Hint{Key: "/", Desc: "Filter"},
		Hint{Key: "shift-n", Desc: "Sort Name"},
		Hint{Key: "shift-a", Desc: "Sort Age"},
		Hint{Key: "?", Desc: "Help"},
	)
	return h
}

func (v *TableView) selected() (Row, bool) {
	r, _ := v.GetSelection()
	idx := r - 1
	if idx < 0 || idx >= len(v.visible) {
		return Row{}, false
	}
	return v.visible[idx], true
}

func (v *TableView) keyboard(ev *tcell.EventKey) *tcell.EventKey {
	row, hasRow := v.selected()
	key, r := ev.Key(), ev.Rune()

	for _, ac := range v.res.Actions {
		if ac.Key != key || (key == tcell.KeyRune && ac.Rune != r) {
			continue
		}
		if ac.Write && !v.app.guardWrite() {
			return nil
		}
		if hasRow {
			ac.Fn(row)
		} else {
			v.app.Flash("warn", "nothing selected")
		}
		return nil
	}
	if key != tcell.KeyRune {
		return ev
	}
	switch {
	case r == 'd' && v.res.Describe != nil && hasRow:
		v.app.Push(newDetailView(v.app, "Describe", row.ID, func(ctx context.Context) (string, error) {
			return v.res.Describe(ctx, row)
		}))
		return nil
	case r == 'y' && v.res.YAML != nil && hasRow:
		v.app.Push(newDetailView(v.app, "YAML", row.ID, func(ctx context.Context) (string, error) {
			obj, err := v.res.YAML(ctx, row)
			if err != nil {
				return "", err
			}
			return toYAML(obj)
		}))
		return nil
	case r == 'c' && hasRow:
		v.app.copyText(row.Cells[0])
		return nil
	case r == 'N':
		v.toggleSort(v.colIndex("NAME"))
		return nil
	case r == 'A':
		v.toggleSort(v.colIndex("AGE", "STARTED", "CREATED"))
		return nil
	case r == 'S':
		v.toggleSort(v.colIndex("STATUS", "STATE", "RUNNING"))
		return nil
	case v.res.RGKeys && r >= '0' && r <= '9':
		v.app.switchRGIndex(int(r - '0'))
		return nil
	}
	return ev
}

func (v *TableView) colIndex(names ...string) int {
	for _, n := range names {
		for i, c := range v.res.Columns {
			if c.Name == n {
				return i
			}
		}
	}
	return -1
}

func (v *TableView) toggleSort(col int) {
	if col < 0 {
		return
	}
	if v.sortCol == col {
		v.sortAsc = !v.sortAsc
	} else {
		v.sortCol, v.sortAsc = col, true
	}
	v.render()
}

func (v *TableView) setTitle() {
	title := fmt.Sprintf(" [%s::b]%s", colorTitle, v.res.Title)
	if v.res.Scope != "" {
		title += fmt.Sprintf("([%s::b]%s[%s::b])", colorScope, tview.Escape(v.res.Scope), colorTitle)
	}
	title += fmt.Sprintf("[%s::b]%s", colorCount, tview.Escape(fmt.Sprintf("[%d]", len(v.visible))))
	if v.filter != "" {
		title += fmt.Sprintf(" [%s::b]%s", colorFilter, tview.Escape("</"+v.filter+">"))
	}
	if v.loadedAt.IsZero() && v.err == nil {
		title += fmt.Sprintf(" [%s::-]loading…", colorCount)
	}
	v.SetTitle(title + " ")
}

func (v *TableView) matcher() func(Row) bool {
	f := v.filter
	if f == "" {
		return func(Row) bool { return true }
	}
	invert := strings.HasPrefix(f, "!")
	f = strings.TrimPrefix(f, "!")
	re, err := regexp.Compile("(?i)" + f)
	match := func(s string) bool {
		if err != nil {
			return strings.Contains(strings.ToLower(s), strings.ToLower(f))
		}
		return re.MatchString(s)
	}
	return func(r Row) bool {
		return match(strings.Join(r.Cells, " ")) != invert
	}
}

func (v *TableView) render() {
	prevID := ""
	if r, ok := v.selected(); ok {
		prevID = r.ID
	}
	match := v.matcher()
	v.visible = v.visible[:0]
	for _, r := range v.rows {
		if match(r) {
			v.visible = append(v.visible, r)
		}
	}
	v.sortRows()

	v.Clear()
	for c, col := range v.res.Columns {
		name := col.Name
		if c == v.sortCol {
			if v.sortAsc {
				name += "↑"
			} else {
				name += "↓"
			}
		}
		cell := tview.NewTableCell(name).SetTextColor(colorHeaderFg).SetAttributes(tcell.AttrBold).
			SetSelectable(false).SetExpansion(1)
		if col.Kind == ColNum {
			cell.SetAlign(tview.AlignRight)
		}
		v.SetCell(0, c, cell)
	}
	sel := 1
	for i, r := range v.visible {
		color := statusColor(r.Status)
		for c, col := range v.res.Columns {
			text := ""
			if c < len(r.Cells) {
				text = r.Cells[c]
			}
			if col.Kind == ColAge {
				text = age(text)
			}
			cell := tview.NewTableCell(tview.Escape(text)).SetTextColor(color).SetExpansion(1).SetMaxWidth(60)
			if col.Kind == ColNum {
				cell.SetAlign(tview.AlignRight)
			}
			v.SetCell(i+1, c, cell)
		}
		if r.ID == prevID {
			sel = i + 1
		}
	}
	if len(v.visible) > 0 {
		v.Select(sel, 0)
	}
	if v.err != nil && len(v.rows) == 0 {
		v.SetCell(1, 0, tview.NewTableCell(tview.Escape(azure.ErrorMessage(v.err))).SetTextColor(colorError).SetSelectable(false))
	}
	v.setTitle()
}

func (v *TableView) sortRows() {
	col := v.sortCol
	if col < 0 || col >= len(v.res.Columns) {
		return
	}
	kind := v.res.Columns[col].Kind
	less := func(a, b string) bool {
		switch kind {
		case ColNum:
			x, _ := strconv.ParseFloat(strings.Fields(a + " 0")[0], 64)
			y, _ := strconv.ParseFloat(strings.Fields(b + " 0")[0], 64)
			return x < y
		case ColAge:
			// newer timestamp = smaller age
			return a > b
		}
		return strings.ToLower(a) < strings.ToLower(b)
	}
	sort.SliceStable(v.visible, func(i, j int) bool {
		a, b := cellAt(v.visible[i], col), cellAt(v.visible[j], col)
		if v.sortAsc {
			return less(a, b)
		}
		return less(b, a)
	})
}

func cellAt(r Row, i int) string {
	if i < len(r.Cells) {
		return r.Cells[i]
	}
	return ""
}

// ts formats a time for an age column.
func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// age renders an RFC3339 timestamp as a k9s-style duration.
func age(s string) string {
	if s == "" {
		return "n/a"
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return humanDuration(time.Since(t))
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
	return fmt.Sprintf("%dy%dd", int(d.Hours())/(24*365), (int(d.Hours())/24)%365)
}
