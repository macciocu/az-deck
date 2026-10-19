package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"sigs.k8s.io/yaml"

	"github.com/macciocu/az-deck/internal/azure"
)

// DetailView shows a describe / YAML document with search (/, n, N).
type DetailView struct {
	*tview.TextView
	app     *App
	kind    string
	subject string
	load    func(ctx context.Context) (string, error)
	text    string
	filter  string
	matches int
	current int
	wrap    bool
}

func newDetailView(a *App, kind, subject string, load func(ctx context.Context) (string, error)) *DetailView {
	v := &DetailView{TextView: tview.NewTextView(), app: a, kind: kind, subject: subject, load: load}
	v.SetDynamicColors(true).SetRegions(true).SetWrap(false).SetScrollable(true)
	v.SetBorder(true).SetBorderPadding(0, 0, 1, 1).SetBorderColor(colorBorderFoc)
	v.SetInputCapture(v.keyboard)
	v.setTitle()
	v.SetText("[papayawhip]loading…")
	return v
}

// Crumb implements Component.
func (v *DetailView) Crumb() string { return strings.ToLower(v.kind) }

// Start loads the document.
func (v *DetailView) Start() {
	if v.text != "" {
		return
	}
	v.Refresh()
}

// Stop implements Component.
func (v *DetailView) Stop() {}

// Refresh reloads the document.
func (v *DetailView) Refresh() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		text, err := v.load(ctx)
		v.app.QueueUpdateDraw(func() {
			if err != nil {
				v.SetText("[orangered]" + tview.Escape(azure.ErrorMessage(err)))
				return
			}
			v.text = text
			v.render()
		})
	}()
}

// Hints implements Component.
func (v *DetailView) Hints() []Hint {
	return []Hint{
		{Key: "esc", Desc: "Back"},
		{Key: "/", Desc: "Search"},
		{Key: "n", Desc: "Next Match"},
		{Key: "shift-n", Desc: "Prev Match"},
		{Key: "c", Desc: "Copy"},
		{Key: "w", Desc: "Toggle Wrap"},
		{Key: "ctrl-s", Desc: "Save"},
		{Key: "j/k", Desc: "Down/Up"},
		{Key: "g/G", Desc: "Top/Bottom"},
		{Key: "ctrl-f/b", Desc: "Page Down/Up"},
	}
}

// Filter implements Filterable.
func (v *DetailView) Filter() string { return v.filter }

// SetFilter implements Filterable (search highlight).
func (v *DetailView) SetFilter(f string) {
	v.filter = f
	v.current = 0
	v.render()
}

func (v *DetailView) setTitle() {
	t := fmt.Sprintf(" [%s::b]%s([%s::b]%s[%s::b])", colorTitle, v.kind, colorScope, tview.Escape(v.subject), colorTitle)
	if v.filter != "" {
		t += fmt.Sprintf(" [%s::b]%s", colorFilter, tview.Escape(fmt.Sprintf("</%s> [%d:%d]", v.filter, min(v.current+1, v.matches), v.matches)))
	}
	v.SetTitle(t + " ")
}

func (v *DetailView) render() {
	if v.filter == "" {
		v.matches = 0
		v.SetText(colorizeYAML(v.text))
		v.Highlight()
		v.setTitle()
		return
	}
	re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(v.filter))
	if err != nil {
		return
	}
	var b strings.Builder
	n := 0
	for _, line := range strings.Split(v.text, "\n") {
		last := 0
		for _, m := range re.FindAllStringIndex(line, -1) {
			b.WriteString(tview.Escape(line[last:m[0]]))
			fmt.Fprintf(&b, `["m%d"][black:yellow]%s[-:-][""]`, n, tview.Escape(line[m[0]:m[1]]))
			last = m[1]
			n++
		}
		b.WriteString(tview.Escape(line[last:]))
		b.WriteByte('\n')
	}
	v.matches = n
	v.SetText(b.String())
	v.jump()
}

func (v *DetailView) jump() {
	if v.matches == 0 {
		v.Highlight()
		v.setTitle()
		return
	}
	v.current = ((v.current % v.matches) + v.matches) % v.matches
	v.Highlight(fmt.Sprintf("m%d", v.current)).ScrollToHighlight()
	v.setTitle()
}

func (v *DetailView) keyboard(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyCtrlS:
		v.app.saveText(v.kind+"-"+v.subject, v.text)
		return nil
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'n':
			v.current++
			v.jump()
			return nil
		case 'N':
			v.current--
			v.jump()
			return nil
		case 'c':
			v.app.copyText(v.text)
			return nil
		case 'w':
			v.wrap = !v.wrap
			v.SetWrap(v.wrap)
			return nil
		}
	}
	return ev
}

var (
	yamlKeyRe = regexp.MustCompile(`^(\s*)(- )?([^\s:#][^:]*?):(\s+|$)(.*)$`)
	yamlCmtRe = regexp.MustCompile(`^\s*#`)
)

// colorizeYAML adds k9s-style colors to YAML / describe output.
func colorizeYAML(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		switch {
		case yamlCmtRe.MatchString(line):
			fmt.Fprintf(&b, "[%s]%s[-]", colorYAMLCmt, tview.Escape(line))
		case yamlKeyRe.MatchString(line):
			m := yamlKeyRe.FindStringSubmatch(line)
			fmt.Fprintf(&b, "%s[white]%s[%s::b]%s[white::-]:%s[%s]%s[-]",
				m[1], m[2], colorYAMLKey, tview.Escape(m[3]), m[4], colorYAMLVal, tview.Escape(m[5]))
		default:
			fmt.Fprintf(&b, "[%s]%s[-]", colorYAMLVal, tview.Escape(line))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// toYAML converts an SDK object to YAML through its JSON representation.
func toYAML(obj any) (string, error) {
	raw, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	out, err := yaml.JSONToYAML(raw)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// saveText dumps text into the azd dumps directory.
func (a *App) saveText(name, text string) {
	dir := filepath.Join(os.TempDir(), "azd-dumps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.Flash("err", "save: %s", err)
		return
	}
	safe := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(name, "_")
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.txt", safe, time.Now().Unix()))
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		a.Flash("err", "save: %s", err)
		return
	}
	a.Flash("info", "saved to %s", path)
}
