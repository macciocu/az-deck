package ui

import (
	"context"
	"strings"
	"time"

	"github.com/macciocu/az-deck/internal/azure"
)

type command struct {
	names []string
	desc  string
	run   func(a *App, arg string)
}

var commands []command

func init() {
	commands = []command{
		{[]string{"apps", "app", "ca", "containerapps", "containerapp", "dp", "deploy"}, "Container apps [rg]", func(a *App, arg string) {
			if arg != "" {
				a.SetScope(Scope{RG: arg})
			}
			a.Reset(a.appsView())
		}},
		{[]string{"rev", "revs", "revision", "revisions", "rs"}, "Revisions of <app>", func(a *App, arg string) {
			a.withApp(arg, func(app azure.App) { a.Reset(a.revisionsView(app.RG, app.Name)) })
		}},
		{[]string{"rep", "replicas", "replica", "po", "pods", "pod"}, "Running replicas of <app>", func(a *App, arg string) {
			a.withApp(arg, func(app azure.App) { a.Reset(a.replicasView(app.RG, app.Name, "")) })
		}},
		{[]string{"env", "envs", "cae", "managedenvironments", "environments"}, "Managed environments [rg]", func(a *App, arg string) {
			if arg != "" {
				a.SetScope(Scope{RG: arg})
			}
			a.Reset(a.envView())
		}},
		{[]string{"jobs", "job", "cj", "cronjobs", "jo"}, "Container app jobs [rg]", func(a *App, arg string) {
			if arg != "" {
				a.SetScope(Scope{RG: arg})
			}
			a.Reset(a.jobsView())
		}},
		{[]string{"rg", "rgs", "resourcegroups", "resourcegroup", "ns", "namespaces"}, "Resource groups (or :rg <name> to scope)", func(a *App, arg string) {
			if arg != "" {
				a.SetScope(Scope{RG: arg})
				a.Reset(a.appsView())
				return
			}
			a.Reset(a.rgView())
		}},
		{[]string{"sub", "subs", "subscriptions", "subscription", "ctx", "context", "contexts"}, "Subscriptions (or :sub <name|id>)", func(a *App, arg string) {
			if arg != "" {
				a.switchSubscriptionByName(arg)
				return
			}
			a.Reset(a.subsView())
		}},
		{[]string{"help", "h"}, "Show help", func(a *App, _ string) {
			a.Push(newHelpView(a, a.top()))
		}},
		{[]string{"q", "quit", "q!", "exit"}, "Quit azd", func(a *App, _ string) {
			a.tv.Stop()
		}},
	}
}

func commandNames() []string {
	var out []string
	for _, c := range commands {
		out = append(out, c.names...)
	}
	return out
}

func commandHints() []Hint {
	var out []Hint
	for _, c := range commands {
		out = append(out, Hint{Key: ":" + c.names[0], Desc: c.desc + "  (" + strings.Join(c.names[1:min(len(c.names), 4)], ",") + ")"})
	}
	return out
}

// runCommand executes a ":" command; it reports whether the command exists.
func (a *App) runCommand(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	name, arg := strings.ToLower(fields[0]), ""
	if len(fields) > 1 {
		arg = fields[1]
	}
	for _, c := range commands {
		for _, n := range c.names {
			if n == name {
				c.run(a, arg)
				return true
			}
		}
	}
	return false
}

// withApp resolves an app by name (in the current scope) and calls fn on the UI goroutine.
func (a *App) withApp(name string, fn func(azure.App)) {
	if name == "" {
		a.Flash("warn", "usage: specify an app name, e.g. :rev my-app")
		return
	}
	scope := a.Scope()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		apps, err := a.az.ListApps(ctx, scope.RG, "")
		a.QueueUpdateDraw(func() {
			if err != nil {
				a.Flash("err", "%s", azure.ErrorMessage(err))
				return
			}
			for _, app := range apps {
				if strings.EqualFold(app.Name, name) {
					fn(app)
					return
				}
			}
			a.Flash("err", "container app %q not found", name)
		})
	}()
}

// switchRGIndex implements the k9s-style numeric namespace shortcuts.
func (a *App) switchRGIndex(i int) {
	if i == 0 {
		a.SetScope(Scope{})
	} else {
		recent := a.state.RecentRGs
		if i > len(recent) {
			return
		}
		a.SetScope(Scope{RG: recent[i-1]})
	}
	if t, ok := a.top().(*TableView); ok {
		// Rebuild the current root view with the new scope.
		switch t.res.Title {
		case "Environments":
			a.Reset(a.envView())
		case "Jobs":
			a.Reset(a.jobsView())
		default:
			a.Reset(a.appsView())
		}
	}
}

func (a *App) switchSubscriptionByName(name string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		subs, err := a.az.ListSubscriptions(ctx)
		a.QueueUpdateDraw(func() {
			if err != nil {
				a.Flash("err", "%s", azure.ErrorMessage(err))
				return
			}
			for _, s := range subs {
				if strings.EqualFold(s.ID, name) || strings.EqualFold(s.Name, name) {
					a.switchSubscription(s)
					return
				}
			}
			a.Flash("err", "subscription %q not found", name)
		})
	}()
}

func (a *App) switchSubscription(s azure.Subscription) {
	if err := a.az.UseSubscription(s); err != nil {
		a.Flash("err", "%s", err)
		return
	}
	a.state.Subscription = s.ID
	a.SetScope(Scope{})
	a.Reset(a.appsView())
	a.Flash("info", "switched to subscription %s", s.Name)
}
