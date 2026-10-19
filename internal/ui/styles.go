package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Colors follow the default k9s skin.
var (
	colorBg        = tcell.ColorDefault
	colorBorder    = tcell.ColorDodgerBlue
	colorBorderFoc = tcell.ColorLightSkyBlue
	colorTitle     = "aqua"
	colorCount     = "papayawhip"
	colorScope     = "fuchsia"
	colorFilter    = "seagreen"
	colorTableFg   = tcell.ColorAqua
	colorHeaderFg  = tcell.ColorWhite
	colorCursorBg  = tcell.ColorAqua
	colorCursorFg  = tcell.ColorBlack
	colorPending   = tcell.ColorOrange
	colorError     = tcell.ColorOrangeRed
	colorDone      = tcell.ColorLightSlateGray
	colorModified  = tcell.ColorGreenYellow
	colorLogo      = "orange"
	colorInfoKey   = "orange"
	colorInfoVal   = "white"
	colorMenuKey   = "dodgerblue"
	colorMenuNum   = "fuchsia"
	colorMenuDesc  = "white"
	colorYAMLKey   = "steelblue"
	colorYAMLVal   = "papayawhip"
	colorYAMLCmt   = "gray"
)

func initStyles() {
	tview.Styles.PrimitiveBackgroundColor = colorBg
	tview.Styles.ContrastBackgroundColor = tcell.ColorDarkSlateGray
	tview.Styles.MoreContrastBackgroundColor = tcell.ColorDodgerBlue
	tview.Styles.BorderColor = colorBorder
	tview.Styles.TitleColor = colorTableFg
	tview.Styles.GraphicsColor = colorBorder
	tview.Styles.PrimaryTextColor = tcell.ColorWhite
	tview.Styles.SecondaryTextColor = tcell.ColorOrange
	tview.Styles.TertiaryTextColor = tcell.ColorFuchsia
	tview.Styles.InverseTextColor = tcell.ColorBlack
	tview.Styles.ContrastSecondaryTextColor = tcell.ColorDodgerBlue

	// k9s draws single-line borders whether or not a view has focus.
	b := &tview.Borders
	b.HorizontalFocus, b.VerticalFocus = b.Horizontal, b.Vertical
	b.TopLeftFocus, b.TopRightFocus = b.TopLeft, b.TopRight
	b.BottomLeftFocus, b.BottomRightFocus = b.BottomLeft, b.BottomRight
}

// statusColor maps a resource status to a k9s-like row color.
func statusColor(status string) tcell.Color {
	s := strings.ToLower(status)
	switch {
	case s == "":
		return colorTableFg
	case containsAny(s, "fail", "error", "unhealthy", "degraded", "crash", "backoff", "terminated", "canceled", "cancelled"):
		return colorError
	case containsAny(s, "progress", "provisioning", "pending", "processing", "activating", "deprovisioning", "scaling", "waiting", "starting", "stopping", "deleting", "updating", "upgrading", "creating", "notstarted", "unknown"):
		return colorPending
	case containsAny(s, "stopped", "inactive", "deprovisioned", "disabled", "deactivated", "succeeded-done", "completed"):
		return colorDone
	}
	return colorTableFg
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}
