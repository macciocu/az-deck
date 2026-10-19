package ui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/atotto/clipboard"
	"github.com/gdamore/tcell/v2"
	"github.com/muesli/cancelreader"
	"github.com/pkg/browser"
	"github.com/rivo/tview"
	"golang.org/x/term"
	"sigs.k8s.io/yaml"

	"github.com/macciocu/az-deck/internal/azure"
)

// ---- Shell ----------------------------------------------------------------

// shell suspends the UI and attaches an interactive shell to the container.
func (a *App) shell(ct azure.Container) {
	var err error
	a.tv.Suspend(func() {
		err = runShell(a.az, ct, a.opts.Shell)
	})
	if err != nil {
		a.Flash("err", "shell: %s", azure.ErrorMessage(err))
		return
	}
	a.Flash("info", "shell session to %s ended", ct.Name)
}

func runShell(az *azure.Client, ct azure.Container, command string) error {
	fmt.Printf("\033[2J\033[H\033[36mazd\033[0m connecting to %s/%s (%s)…\r\n", shortReplica(ct.Replica), ct.Name, command)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	conn, err := az.Exec(ctx, ct, command)
	cancel()
	if err != nil {
		return err
	}
	defer conn.Close()

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		old, err := term.MakeRaw(fd)
		if err != nil {
			return err
		}
		defer term.Restore(fd, old)
		sendSize := func() {
			if w, h, err := term.GetSize(fd); err == nil {
				_ = conn.Resize(w, h)
			}
		}
		sendSize()
		stop := watchResize(sendSize)
		defer stop()
	}

	cr, err := cancelreader.NewReader(os.Stdin)
	if err != nil {
		return err
	}
	defer cr.Close()
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := cr.Read(buf)
			if n > 0 {
				if _, werr := conn.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	err = conn.Pump(os.Stdout, os.Stderr)
	cr.Cancel()
	return err
}

// ---- Edit -----------------------------------------------------------------

const editHeader = `# Editing %s. Saving applies the change as a PATCH (merge) on the resource.
# Secrets and registry credentials are omitted and left untouched.
# Close the editor without changes to cancel.
`

// appEdit is the editable subset of a container app.
type appEdit struct {
	Tags          map[string]*string         `json:"tags,omitempty"`
	Configuration *appEditConfig             `json:"configuration,omitempty"`
	Template      *armappcontainers.Template `json:"template,omitempty"`
}

type appEditConfig struct {
	ActiveRevisionsMode  *armappcontainers.ActiveRevisionsMode `json:"activeRevisionsMode,omitempty"`
	MaxInactiveRevisions *int32                                `json:"maxInactiveRevisions,omitempty"`
	Ingress              *armappcontainers.Ingress             `json:"ingress,omitempty"`
	Dapr                 *armappcontainers.Dapr                `json:"dapr,omitempty"`
}

func (a *App) editApp(ap azure.App) {
	a.fetchThen(func(ctx context.Context) (any, error) { return a.az.GetApp(ctx, ap.RG, ap.Name) }, func(v any) {
		raw := v.(*armappcontainers.ContainerApp)
		doc := appEdit{Tags: raw.Tags}
		if p := raw.Properties; p != nil {
			doc.Template = p.Template
			if c := p.Configuration; c != nil {
				doc.Configuration = &appEditConfig{ActiveRevisionsMode: c.ActiveRevisionsMode,
					MaxInactiveRevisions: c.MaxInactiveRevisions, Ingress: c.Ingress, Dapr: c.Dapr}
			}
		}
		a.editLoop("containerapp "+ap.Name, doc, func(edited []byte) (func(context.Context) error, error) {
			var out appEdit
			if err := json.Unmarshal(edited, &out); err != nil {
				return nil, err
			}
			patch := armappcontainers.ContainerApp{Location: raw.Location, Tags: out.Tags,
				Properties: &armappcontainers.ContainerAppProperties{Template: out.Template}}
			if c := out.Configuration; c != nil {
				patch.Properties.Configuration = &armappcontainers.Configuration{ActiveRevisionsMode: c.ActiveRevisionsMode,
					MaxInactiveRevisions: c.MaxInactiveRevisions, Ingress: c.Ingress, Dapr: c.Dapr}
			}
			return func(ctx context.Context) error { return a.az.UpdateApp(ctx, ap.RG, ap.Name, patch) }, nil
		})
	})
}

type jobEdit struct {
	Tags          map[string]*string            `json:"tags,omitempty"`
	Configuration *jobEditConfig                `json:"configuration,omitempty"`
	Template      *armappcontainers.JobTemplate `json:"template,omitempty"`
}

type jobEditConfig struct {
	TriggerType           *armappcontainers.TriggerType                           `json:"triggerType,omitempty"`
	ReplicaTimeout        *int32                                                  `json:"replicaTimeout,omitempty"`
	ReplicaRetryLimit     *int32                                                  `json:"replicaRetryLimit,omitempty"`
	ScheduleTriggerConfig *armappcontainers.JobConfigurationScheduleTriggerConfig `json:"scheduleTriggerConfig,omitempty"`
	ManualTriggerConfig   *armappcontainers.JobConfigurationManualTriggerConfig   `json:"manualTriggerConfig,omitempty"`
	EventTriggerConfig    *armappcontainers.JobConfigurationEventTriggerConfig    `json:"eventTriggerConfig,omitempty"`
}

func (a *App) editJob(j azure.Job) {
	a.fetchThen(func(ctx context.Context) (any, error) { return a.az.GetJob(ctx, j.RG, j.Name) }, func(v any) {
		raw := v.(*armappcontainers.Job)
		doc := jobEdit{Tags: raw.Tags}
		if p := raw.Properties; p != nil {
			doc.Template = p.Template
			if c := p.Configuration; c != nil {
				doc.Configuration = &jobEditConfig{TriggerType: c.TriggerType, ReplicaTimeout: c.ReplicaTimeout,
					ReplicaRetryLimit: c.ReplicaRetryLimit, ScheduleTriggerConfig: c.ScheduleTriggerConfig,
					ManualTriggerConfig: c.ManualTriggerConfig, EventTriggerConfig: c.EventTriggerConfig}
			}
		}
		a.editLoop("job "+j.Name, doc, func(edited []byte) (func(context.Context) error, error) {
			var out jobEdit
			if err := json.Unmarshal(edited, &out); err != nil {
				return nil, err
			}
			patch := armappcontainers.JobPatchProperties{Tags: out.Tags,
				Properties: &armappcontainers.JobPatchPropertiesProperties{Template: out.Template}}
			if c := out.Configuration; c != nil {
				patch.Properties.Configuration = &armappcontainers.JobConfiguration{TriggerType: c.TriggerType,
					ReplicaTimeout: c.ReplicaTimeout, ReplicaRetryLimit: c.ReplicaRetryLimit,
					ScheduleTriggerConfig: c.ScheduleTriggerConfig, ManualTriggerConfig: c.ManualTriggerConfig,
					EventTriggerConfig: c.EventTriggerConfig}
			}
			return func(ctx context.Context) error { return a.az.UpdateJob(ctx, j.RG, j.Name, patch) }, nil
		})
	})
}

// fetchThen loads something off the UI goroutine and continues on it.
func (a *App) fetchThen(fetch func(context.Context) (any, error), then func(any)) {
	a.Flash("info", "fetching…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		v, err := fetch(ctx)
		a.QueueUpdateDraw(func() {
			if err != nil {
				a.Flash("err", "%s", azure.ErrorMessage(err))
				return
			}
			then(v)
		})
	}()
}

// editLoop opens doc as YAML in $EDITOR until it parses or is left unchanged,
// then applies it asynchronously.
func (a *App) editLoop(what string, doc any, parse func(json []byte) (func(context.Context) error, error)) {
	y, err := toYAML(doc)
	if err != nil {
		a.Flash("err", "%s", err)
		return
	}
	original := fmt.Sprintf(editHeader, what) + y
	f, err := os.CreateTemp("", "azd-edit-*.yaml")
	if err != nil {
		a.Flash("err", "%s", err)
		return
	}
	path := f.Name()
	defer os.Remove(path)
	_, _ = f.WriteString(original)
	f.Close()

	var apply func(context.Context) error
	var editErr error
	cancelled := false
	a.tv.Suspend(func() {
		for {
			if editErr = runEditor(path); editErr != nil {
				return
			}
			edited, err := os.ReadFile(path)
			if err != nil {
				editErr = err
				return
			}
			body := stripComments(edited)
			if bytes.Equal(body, stripComments([]byte(original))) {
				cancelled = true
				return
			}
			js, err := yaml.YAMLToJSON(body)
			if err == nil {
				apply, err = parse(js)
			}
			if err == nil {
				return
			}
			// Re-open with the error on top, like kubectl edit.
			msg := "# ERROR: " + strings.ReplaceAll(err.Error(), "\n", "\n# ") + "\n"
			_ = os.WriteFile(path, append([]byte(msg), body...), 0o600)
		}
	})
	switch {
	case editErr != nil:
		a.Flash("err", "edit: %s", editErr)
	case cancelled:
		a.Flash("info", "edit cancelled, no changes")
	default:
		a.Async("Updating "+what, apply)
	}
}

func stripComments(b []byte) []byte {
	var out [][]byte
	for _, l := range bytes.Split(b, []byte("\n")) {
		if !bytes.HasPrefix(bytes.TrimSpace(l), []byte("#")) {
			out = append(out, l)
		}
	}
	return bytes.TrimSpace(bytes.Join(out, []byte("\n")))
}

func runEditor(path string) error {
	editor := firstNonEmpty(os.Getenv("AZD_EDITOR"), os.Getenv("KUBE_EDITOR"), os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	cmd := exec.Command("sh", "-c", editor+` "$1"`, "azd-editor", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---- Scale ----------------------------------------------------------------

func (a *App) scaleDialog(ap azure.App) {
	form := tview.NewForm()
	minS, maxS := strconv.Itoa(int(ap.MinReplicas)), strconv.Itoa(int(ap.MaxReplicas))
	numeric := func(text string, _ rune) bool { _, err := strconv.Atoi(text); return text == "" || err == nil }
	form.AddInputField("Min replicas:", minS, 6, numeric, func(t string) { minS = t })
	form.AddInputField("Max replicas:", maxS, 6, numeric, func(t string) { maxS = t })
	form.AddButton("OK", func() {
		lo, err1 := strconv.Atoi(minS)
		hi, err2 := strconv.Atoi(maxS)
		if err := errors.Join(err1, err2); err != nil || lo < 0 || hi < 1 || lo > hi {
			a.Flash("err", "invalid replica range %q-%q", minS, maxS)
			return
		}
		a.closeDialog("scale")
		a.Async(fmt.Sprintf("Scaling %s to %d-%d", ap.Name, lo, hi), func(ctx context.Context) error {
			return a.az.ScaleApp(ctx, ap.RG, ap.Name, int32(lo), int32(hi))
		})
	})
	form.AddButton("Cancel", func() { a.closeDialog("scale") })
	form.SetCancelFunc(func() { a.closeDialog("scale") })
	form.SetFieldBackgroundColor(tcell.ColorDarkSlateGray).
		SetButtonBackgroundColor(tcell.ColorDodgerBlue).
		SetButtonTextColor(tcell.ColorBlack).
		SetLabelColor(tcell.ColorAqua)
	form.SetBorder(true).SetTitle(" <Scale " + ap.Name + "> ").SetTitleColor(tcell.ColorAqua)
	a.showDialog("scale", form, 50, 9)
}

// ---- Clipboard / browser --------------------------------------------------

func (a *App) copyText(s string) {
	if err := clipboard.WriteAll(s); err != nil {
		// Fall back to OSC 52, which most modern terminals (incl. over SSH/WSL) support.
		tty, terr := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
		if terr != nil {
			a.Flash("err", "copy failed: %s", err)
			return
		}
		fmt.Fprintf(tty, "\033]52;c;%s\a", base64.StdEncoding.EncodeToString([]byte(s)))
		tty.Close()
	}
	a.Flash("info", "copied %d bytes to clipboard", len(s))
}

func (a *App) browse(url string) {
	browser.Stdout, browser.Stderr = io.Discard, io.Discard
	if err := browser.OpenURL(url); err != nil {
		a.Flash("err", "open %s: %s", url, err)
		return
	}
	a.Flash("info", "opened %s", url)
}
