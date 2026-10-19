package ui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/macciocu/az-deck/internal/azure"
)

const maxLogLines = 5000

var logColors = []string{"aqua", "greenyellow", "fuchsia", "orange", "dodgerblue", "gold", "violet", "springgreen"}

// LogSource is one stream feeding a log view.
type LogSource struct {
	Label string
	Run   func(ctx context.Context, tail int, emit func(string)) error
}

type logLine struct {
	src  int
	text string
}

// LogView streams and displays logs from one or more sources.
type LogView struct {
	*tview.TextView
	app     *App
	subject string
	sources []LogSource
	tail    int

	mu      sync.Mutex
	pending []logLine

	lines      []logLine
	filter     string
	autoscroll bool
	wrap       bool
	showSrc    bool
	cancel     context.CancelFunc
	status     string
}

func newLogView(a *App, subject string, sources []LogSource) *LogView {
	v := &LogView{TextView: tview.NewTextView(), app: a, subject: subject, sources: sources,
		tail: 100, autoscroll: true, showSrc: len(sources) > 1}
	v.SetDynamicColors(true).SetScrollable(true).SetWrap(false).SetMaxLines(maxLogLines)
	v.SetBorder(true).SetBorderPadding(0, 0, 1, 1).SetBorderColor(colorBorderFoc)
	v.SetInputCapture(v.keyboard)
	v.setTitle()
	return v
}

// Crumb implements Component.
func (v *LogView) Crumb() string { return "logs" }

// Hints implements Component.
func (v *LogView) Hints() []Hint {
	return []Hint{
		{Key: "esc", Desc: "Back"},
		{Key: "s", Desc: "Toggle AutoScroll"},
		{Key: "w", Desc: "Toggle Wrap"},
		{Key: "p", Desc: "Toggle Prefix"},
		{Key: "c", Desc: "Clear"},
		{Key: "ctrl-s", Desc: "Save"},
		{Key: "/", Desc: "Filter"},
		{Key: "1", Desc: "tail 50", Num: true},
		{Key: "2", Desc: "tail 100", Num: true},
		{Key: "3", Desc: "tail 300", Num: true},
		{Key: "0", Desc: "tail none", Num: true},
	}
}

// Start opens the streams.
func (v *LogView) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	v.status = "streaming"
	v.setTitle()
	var wg sync.WaitGroup
	for i, src := range v.sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := src.Run(ctx, v.tail, func(s string) { v.push(i, s) })
			if err != nil && ctx.Err() == nil {
				v.push(i, fmt.Sprintf("[azd] stream error: %s", azure.ErrorMessage(err)))
			}
		}()
	}
	go func() {
		wg.Wait()
		if ctx.Err() == nil {
			v.app.QueueUpdateDraw(func() {
				v.status = "stream closed"
				v.setTitle()
			})
		}
	}()
	go v.flushLoop(ctx)
}

// Stop closes the streams.
func (v *LogView) Stop() {
	if v.cancel != nil {
		v.cancel()
	}
}

// Refresh restarts the streams.
func (v *LogView) Refresh() {
	v.Stop()
	v.clear()
	v.Start()
}

// Filter implements Filterable.
func (v *LogView) Filter() string { return v.filter }

// SetFilter implements Filterable.
func (v *LogView) SetFilter(f string) {
	v.filter = f
	v.rerender()
}

func (v *LogView) push(src int, text string) {
	v.mu.Lock()
	v.pending = append(v.pending, logLine{src: src, text: text})
	v.mu.Unlock()
}

func (v *LogView) flushLoop(ctx context.Context) {
	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			v.mu.Lock()
			batch := v.pending
			v.pending = nil
			v.mu.Unlock()
			if len(batch) == 0 {
				continue
			}
			v.app.QueueUpdateDraw(func() { v.append(batch) })
		}
	}
}

func (v *LogView) append(batch []logLine) {
	v.lines = append(v.lines, batch...)
	if over := len(v.lines) - maxLogLines; over > 0 {
		v.lines = v.lines[over:]
	}
	match := v.matcher()
	var b strings.Builder
	for _, l := range batch {
		if match(l.text) {
			v.format(&b, l)
		}
	}
	_, _ = v.Write([]byte(b.String()))
	if v.autoscroll {
		v.ScrollToEnd()
	}
}

func (v *LogView) rerender() {
	v.Clear()
	match := v.matcher()
	var b strings.Builder
	for _, l := range v.lines {
		if match(l.text) {
			v.format(&b, l)
		}
	}
	_, _ = v.Write([]byte(b.String()))
	if v.autoscroll {
		v.ScrollToEnd()
	}
	v.setTitle()
}

func (v *LogView) format(b *strings.Builder, l logLine) {
	if v.showSrc && l.src < len(v.sources) {
		fmt.Fprintf(b, "[%s::b]%s[-::-] ", logColors[l.src%len(logColors)], tview.Escape(v.sources[l.src].Label))
	}
	b.WriteString(tview.TranslateANSI(tview.Escape(l.text)))
	b.WriteString("[-:-:-]\n")
}

func (v *LogView) matcher() func(string) bool {
	if v.filter == "" {
		return func(string) bool { return true }
	}
	invert := strings.HasPrefix(v.filter, "!")
	f := strings.TrimPrefix(v.filter, "!")
	re, err := regexp.Compile("(?i)" + f)
	return func(s string) bool {
		var ok bool
		if err != nil {
			ok = strings.Contains(strings.ToLower(s), strings.ToLower(f))
		} else {
			ok = re.MatchString(s)
		}
		return ok != invert
	}
}

func (v *LogView) clear() {
	v.lines = nil
	v.Clear()
}

func (v *LogView) setTitle() {
	onOff := func(b bool) string {
		if b {
			return "On"
		}
		return "Off"
	}
	t := fmt.Sprintf(" [%s::b]Logs([%s::b]%s[%s::b]) [%s::-]Autoscroll:[white]%s [%s::-]Wrap:[white]%s [%s::-]Tail:[white]%d [gray]%s",
		colorTitle, colorScope, tview.Escape(v.subject), colorTitle,
		colorCount, onOff(v.autoscroll), colorCount, onOff(v.wrap), colorCount, v.tail, v.status)
	if v.filter != "" {
		t += fmt.Sprintf(" [%s::b]%s", colorFilter, tview.Escape("</"+v.filter+">"))
	}
	v.SetTitle(t + " ")
}

func (v *LogView) keyboard(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyCtrlS:
		var b strings.Builder
		for _, l := range v.lines {
			if v.showSrc {
				b.WriteString(v.sources[l.src].Label + " ")
			}
			b.WriteString(l.text + "\n")
		}
		v.app.saveText("logs-"+v.subject, b.String())
		return nil
	case tcell.KeyUp, tcell.KeyPgUp, tcell.KeyCtrlB, tcell.KeyHome:
		v.setAutoscroll(false)
		return ev
	case tcell.KeyEnd:
		v.setAutoscroll(true)
		return ev
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'k', 'g':
			v.setAutoscroll(false)
			return ev
		case 'G':
			v.setAutoscroll(true)
			return ev
		case 's':
			v.setAutoscroll(!v.autoscroll)
			return nil
		case 'w':
			v.wrap = !v.wrap
			v.SetWrap(v.wrap)
			v.setTitle()
			return nil
		case 'p':
			v.showSrc = !v.showSrc
			v.rerender()
			return nil
		case 'c':
			v.clear()
			return nil
		case '0', '1', '2', '3':
			v.tail = map[rune]int{'0': 0, '1': 50, '2': 100, '3': azure.MaxTail}[ev.Rune()]
			v.Refresh()
			return nil
		}
	}
	return ev
}

func (v *LogView) setAutoscroll(on bool) {
	if v.autoscroll == on {
		return
	}
	v.autoscroll = on
	if on {
		v.ScrollToEnd()
	}
	v.setTitle()
}
