package ui

import (
	"strings"
	"testing"
	"time"
)

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second:          "30s",
		90 * time.Second:          "1m30s",
		3*time.Hour + time.Minute: "3h1m",
		50 * time.Hour:            "2d2h",
		400 * 24 * time.Hour:      "1y35d",
	}
	for d, want := range cases {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestTableMatcher(t *testing.T) {
	v := &TableView{}
	row := Row{Cells: []string{"nazar-app-prod", "rg-nazar-prod", "Running"}}
	for filter, want := range map[string]bool{
		"":          true,
		"nazar":     true,
		"NAZAR":     true,
		"^nazar":    true,
		"!nazar":    false,
		"!clarion":  true,
		"clarion":   false,
		"bad[regex": false,
	} {
		v.filter = filter
		if got := v.matcher()(row); got != want {
			t.Errorf("filter %q: got %v want %v", filter, got, want)
		}
	}
}

func TestStatusColor(t *testing.T) {
	if statusColor("") != colorTableFg || statusColor("Failed") != colorError ||
		statusColor("Provisioning") != colorPending || statusColor("inactive") != colorDone {
		t.Fatal("unexpected status colors")
	}
}

func TestStripComments(t *testing.T) {
	in := "# header\nfoo: 1\n  # nested\nbar: 2\n"
	if got := string(stripComments([]byte(in))); got != "foo: 1\nbar: 2" {
		t.Fatalf("stripComments = %q", got)
	}
}

func TestColorizeYAMLKeepsText(t *testing.T) {
	out := colorizeYAML("name: app\n# comment\n- item: [x]")
	for _, want := range []string{"name", "app", "comment", "item"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

func TestRenderMenuColumns(t *testing.T) {
	var hints []Hint
	for i := 0; i < 8; i++ {
		hints = append(hints, Hint{Key: "k", Desc: "d"})
	}
	lines := strings.Split(strings.TrimRight(renderMenu(hints), "\n"), "\n")
	if len(lines) != menuRows {
		t.Fatalf("expected %d rows, got %d", menuRows, len(lines))
	}
	if strings.Count(lines[0], "<k>") != 2 || strings.Count(lines[5], "<k>") != 1 {
		t.Fatalf("unexpected layout: %q", lines)
	}
}

func TestCommandsResolve(t *testing.T) {
	names := map[string]bool{}
	for _, n := range commandNames() {
		if names[n] {
			t.Errorf("duplicate command alias %q", n)
		}
		names[n] = true
	}
	for _, n := range []string{"apps", "rev", "rep", "env", "jobs", "rg", "sub", "ctx", "q", "help"} {
		if !names[n] {
			t.Errorf("missing command %q", n)
		}
	}
}

func TestSuggestion(t *testing.T) {
	if got := suggestion("re"); got != "rep" {
		t.Errorf("suggestion(re) = %q", got)
	}
	if got := suggestion("rg"); got != "rgs" {
		t.Errorf("suggestion(rg) = %q", got)
	}
	if got := suggestion("apps x"); got != "" {
		t.Errorf("suggestion with arg = %q", got)
	}
}
