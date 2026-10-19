package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/macciocu/az-deck/internal/azure"
)

func scopeLabel(s Scope) string {
	switch {
	case s.Env != "":
		return s.Env
	case s.RG != "":
		return s.RG
	}
	return "all"
}

func onOff(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func act(key tcell.Key, r rune, label string, write bool, fn func(Row)) Action {
	return Action{Key: key, Rune: r, Label: label, Write: write, Fn: fn}
}

// ---- Subscriptions --------------------------------------------------------

func (a *App) subsView() Component {
	return newTableView(a, &Resource{
		Title:   "Subscriptions",
		Columns: []Column{{Name: "NAME"}, {Name: "ID"}, {Name: "STATE"}, {Name: "TENANT"}},
		List: func(ctx context.Context) ([]Row, error) {
			subs, err := a.az.ListSubscriptions(ctx)
			if err != nil {
				return nil, err
			}
			cur := a.az.Subscription().ID
			rows := make([]Row, 0, len(subs))
			for _, s := range subs {
				name := s.Name
				if s.ID == cur {
					name += "(*)"
				}
				status := s.State
				if status == "Enabled" {
					status = ""
				}
				rows = append(rows, Row{ID: s.ID, Cells: []string{name, s.ID, s.State, s.TenantID}, Status: status, Data: s})
			}
			return rows, nil
		},
		EnterTip: "Use",
		Enter:    func(r Row) { a.switchSubscription(r.Data.(azure.Subscription)) },
		YAML:     func(_ context.Context, r Row) (any, error) { return r.Data.(azure.Subscription).Raw, nil },
	})
}

// ---- Resource groups ------------------------------------------------------

func (a *App) rgView() Component {
	return newTableView(a, &Resource{
		Title:   "ResourceGroups",
		Scope:   a.az.Subscription().Name,
		Columns: []Column{{Name: "NAME"}, {Name: "LOCATION"}, {Name: "STATE"}},
		List: func(ctx context.Context) ([]Row, error) {
			rgs, err := a.az.ListResourceGroups(ctx)
			if err != nil {
				return nil, err
			}
			rows := make([]Row, 0, len(rgs))
			for _, g := range rgs {
				status := g.State
				if status == "Succeeded" {
					status = ""
				}
				rows = append(rows, Row{ID: g.Name, Cells: []string{g.Name, g.Location, g.State}, Status: status, Data: g})
			}
			return rows, nil
		},
		EnterTip: "Scope",
		Enter: func(r Row) {
			a.SetScope(Scope{RG: r.ID})
			a.Reset(a.appsView())
		},
		YAML: func(_ context.Context, r Row) (any, error) { return r.Data.(azure.ResourceGroup).Raw, nil },
	})
}

// ---- Environments ---------------------------------------------------------

func (a *App) envView() Component {
	scope := a.Scope()
	return newTableView(a, &Resource{
		Title:   "Environments",
		Scope:   scopeLabel(Scope{RG: scope.RG}),
		RGKeys:  true,
		Columns: []Column{{Name: "NAME"}, {Name: "RG"}, {Name: "LOCATION"}, {Name: "DOMAIN"}, {Name: "STATIC IP"}, {Name: "STATE"}, {Name: "AGE", Kind: ColAge}},
		List: func(ctx context.Context) ([]Row, error) {
			envs, err := a.az.ListEnvironments(ctx, scope.RG)
			if err != nil {
				return nil, err
			}
			rows := make([]Row, 0, len(envs))
			for _, e := range envs {
				rows = append(rows, Row{ID: e.RG + "/" + e.Name, Status: okStatus(e.State, "Succeeded"), Data: e,
					Cells: []string{e.Name, e.RG, e.Location, e.Domain, e.StaticIP, e.State, ts(e.Created)}})
			}
			return rows, nil
		},
		EnterTip: "Apps",
		Enter: func(r Row) {
			e := r.Data.(azure.Environment)
			a.SetScope(Scope{RG: e.RG, Env: e.Name})
			a.Push(a.appsView())
		},
		Describe: func(_ context.Context, r Row) (string, error) { return describeEnv(r.Data.(azure.Environment)), nil },
		YAML:     func(_ context.Context, r Row) (any, error) { return r.Data.(azure.Environment).Raw, nil },
		Actions: []Action{
			act(tcell.KeyRune, 'l', "Logs (system)", false, func(r Row) {
				e := r.Data.(azure.Environment)
				a.Push(newLogView(a, e.Name, []LogSource{{Label: e.Name, Run: func(ctx context.Context, tail int, emit func(string)) error {
					return a.az.StreamEnvLogs(ctx, e.RG, e.Name, e.EventStream, tail, true, emit)
				}}}))
			}),
			act(tcell.KeyCtrlD, 0, "Delete", true, func(r Row) {
				e := r.Data.(azure.Environment)
				a.Confirm("Delete", fmt.Sprintf("Delete environment %s/%s?\nAll apps in it must be deleted first.", e.RG, e.Name), func() {
					a.Async("Deleting environment "+e.Name, func(ctx context.Context) error { return a.az.DeleteEnvironment(ctx, e.RG, e.Name) })
				})
			}),
		},
	})
}

// ---- Container apps -------------------------------------------------------

func (a *App) appsView() Component {
	scope := a.Scope()
	return newTableView(a, &Resource{
		Title:  "ContainerApps",
		Scope:  scopeLabel(scope),
		RGKeys: true,
		Columns: []Column{{Name: "NAME"}, {Name: "RG"}, {Name: "ENV"}, {Name: "STATUS"}, {Name: "PROVISIONING"},
			{Name: "REVISION"}, {Name: "MODE"}, {Name: "SCALE"}, {Name: "IMAGE"}, {Name: "AGE", Kind: ColAge}},
		List: func(ctx context.Context) ([]Row, error) {
			apps, err := a.az.ListApps(ctx, scope.RG, scope.Env)
			if err != nil {
				return nil, err
			}
			rows := make([]Row, 0, len(apps))
			for _, ap := range apps {
				status := ap.Running
				if s := okStatus(ap.State, "Succeeded"); s != "" {
					status = s
				} else if status == "Running" {
					status = ""
				}
				rows = append(rows, Row{ID: ap.RG + "/" + ap.Name, Status: status, Data: ap, Cells: []string{
					ap.Name, ap.RG, ap.Env, ap.Running, ap.State, ap.Revision, ap.Mode,
					fmt.Sprintf("%d-%d", ap.MinReplicas, ap.MaxReplicas), ap.Image, ts(ap.Created)}})
			}
			return rows, nil
		},
		EnterTip: "Revisions",
		Enter: func(r Row) {
			ap := r.Data.(azure.App)
			a.Push(a.revisionsView(ap.RG, ap.Name))
		},
		Describe: func(ctx context.Context, r Row) (string, error) {
			ap := r.Data.(azure.App)
			raw, err := a.az.GetApp(ctx, ap.RG, ap.Name)
			if err != nil {
				return "", err
			}
			return describeApp(raw), nil
		},
		YAML: func(ctx context.Context, r Row) (any, error) {
			ap := r.Data.(azure.App)
			return a.az.GetApp(ctx, ap.RG, ap.Name)
		},
		Actions: []Action{
			act(tcell.KeyRune, 'l', "Logs", false, func(r Row) { a.appLogs(r.Data.(azure.App)) }),
			act(tcell.KeyRune, 'v', "Events", false, func(r Row) {
				ap := r.Data.(azure.App)
				a.Push(newLogView(a, ap.Name+" (system)", []LogSource{{Label: ap.Name, Run: func(ctx context.Context, tail int, emit func(string)) error {
					return a.az.StreamAppLogs(ctx, ap.RG, ap.Name, ap.EventStream, tail, true, emit)
				}}}))
			}),
			act(tcell.KeyRune, 'x', "Shell", false, func(r Row) { a.appShell(r.Data.(azure.App)) }),
			act(tcell.KeyRune, 'b', "Browse", false, func(r Row) {
				ap := r.Data.(azure.App)
				if ap.FQDN == "" {
					a.Flash("warn", "%s has no ingress FQDN", ap.Name)
					return
				}
				a.browse("https://" + ap.FQDN)
			}),
			act(tcell.KeyRune, 's', "Scale", true, func(r Row) { a.scaleDialog(r.Data.(azure.App)) }),
			act(tcell.KeyRune, 'r', "Restart", true, func(r Row) {
				ap := r.Data.(azure.App)
				a.Confirm("Restart", fmt.Sprintf("Restart all active revisions of %s?", ap.Name), func() {
					a.Async("Restarting "+ap.Name, func(ctx context.Context) error { return a.az.RestartApp(ctx, ap.RG, ap.Name) })
				})
			}),
			act(tcell.KeyCtrlT, 0, "Start/Stop", true, func(r Row) {
				ap := r.Data.(azure.App)
				if strings.EqualFold(ap.Running, "Stopped") {
					a.Confirm("Start", "Start "+ap.Name+"?", func() {
						a.Async("Starting "+ap.Name, func(ctx context.Context) error { return a.az.StartApp(ctx, ap.RG, ap.Name) })
					})
					return
				}
				a.Confirm("Stop", "Stop "+ap.Name+"?", func() {
					a.Async("Stopping "+ap.Name, func(ctx context.Context) error { return a.az.StopApp(ctx, ap.RG, ap.Name) })
				})
			}),
			act(tcell.KeyRune, 'e', "Edit", true, func(r Row) { a.editApp(r.Data.(azure.App)) }),
			act(tcell.KeyCtrlD, 0, "Delete", true, func(r Row) {
				ap := r.Data.(azure.App)
				a.Confirm("Delete", fmt.Sprintf("Delete container app %s/%s?", ap.RG, ap.Name), func() {
					a.Async("Deleting "+ap.Name, func(ctx context.Context) error { return a.az.DeleteApp(ctx, ap.RG, ap.Name) })
				})
			}),
		},
	})
}

// appLogs streams logs of every container in every running replica of the app.
func (a *App) appLogs(ap azure.App) {
	a.withReplicas(ap.RG, ap.Name, "", func(reps []azure.Replica) {
		var cts []azure.Container
		for _, r := range reps {
			cts = append(cts, appContainers(r)...)
		}
		if len(cts) == 0 {
			a.Flash("warn", "%s has no running replicas (try <v> for system events)", ap.Name)
			return
		}
		a.Push(newLogView(a, ap.Name, a.containerSources(cts)))
	})
}

// appShell lets the user pick a replica/container of an app and opens a shell.
func (a *App) appShell(ap azure.App) {
	a.withReplicas(ap.RG, ap.Name, "", func(reps []azure.Replica) {
		var cts []azure.Container
		for _, r := range reps {
			cts = append(cts, appContainers(r)...)
		}
		a.pickContainer(cts, true, a.shell)
	})
}

func (a *App) withReplicas(rg, app, rev string, fn func([]azure.Replica)) {
	a.Flash("info", "fetching replicas of %s…", app)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var reps []azure.Replica
		var err error
		if rev == "" {
			reps, err = a.az.ListActiveReplicas(ctx, rg, app)
		} else {
			reps, err = a.az.ListReplicas(ctx, rg, app, rev)
		}
		a.QueueUpdateDraw(func() {
			if err != nil {
				a.Flash("err", "%s", azure.ErrorMessage(err))
				return
			}
			fn(reps)
		})
	}()
}

func appContainers(r azure.Replica) []azure.Container {
	var out []azure.Container
	for _, c := range r.Containers {
		if !c.Init {
			out = append(out, c)
		}
	}
	return out
}

// pickContainer chooses a container, asking only when there is more than one.
func (a *App) pickContainer(cts []azure.Container, showReplica bool, fn func(azure.Container)) {
	switch len(cts) {
	case 0:
		a.Flash("warn", "no running containers")
		return
	case 1:
		fn(cts[0])
		return
	}
	labels := make([]string, len(cts))
	byLabel := map[string]azure.Container{}
	for i, c := range cts {
		l := c.Name
		if showReplica {
			l = shortReplica(c.Replica) + "/" + c.Name
		}
		labels[i] = l
		byLabel[l] = c
	}
	a.Pick("Container", labels, func(l string) { fn(byLabel[l]) })
}

func shortReplica(name string) string {
	if i := strings.LastIndex(name, "--"); i >= 0 {
		return name[i+2:]
	}
	return name
}

func (a *App) containerSources(cts []azure.Container) []LogSource {
	srcs := make([]LogSource, 0, len(cts))
	for _, c := range cts {
		srcs = append(srcs, LogSource{
			Label: shortReplica(c.Replica) + "/" + c.Name,
			Run: func(ctx context.Context, tail int, emit func(string)) error {
				return a.az.StreamAppLogs(ctx, c.RG, c.App, c.LogStream, tail, true, emit)
			},
		})
	}
	return srcs
}

// ---- Revisions ------------------------------------------------------------

func (a *App) revisionsView(rg, app string) Component {
	return newTableView(a, &Resource{
		Title:   "Revisions",
		Scope:   app,
		SortCol: 7,
		Columns: []Column{{Name: "NAME"}, {Name: "ACTIVE"}, {Name: "TRAFFIC", Kind: ColNum}, {Name: "REPLICAS", Kind: ColNum},
			{Name: "HEALTH"}, {Name: "RUNNING"}, {Name: "PROVISIONING"}, {Name: "AGE", Kind: ColAge}},
		List: func(ctx context.Context) ([]Row, error) {
			revs, err := a.az.ListRevisions(ctx, rg, app)
			if err != nil {
				return nil, err
			}
			rows := make([]Row, 0, len(revs))
			for _, r := range revs {
				status := ""
				switch {
				case !r.Active:
					status = "inactive"
				case okStatus(r.Provisioning, "Provisioned") != "":
					status = r.Provisioning
				case r.Health != "" && r.Health != "Healthy":
					status = r.Health
				}
				rows = append(rows, Row{ID: r.Name, Status: status, Data: r, Cells: []string{
					r.Name, onOff(r.Active), fmt.Sprintf("%d%%", r.Traffic), strconv.Itoa(int(r.Replicas)),
					r.Health, r.Running, r.Provisioning, ts(r.Created)}})
			}
			return rows, nil
		},
		EnterTip: "Replicas",
		Enter: func(r Row) {
			rv := r.Data.(azure.Revision)
			a.Push(a.replicasView(rv.RG, rv.App, rv.Name))
		},
		Describe: func(_ context.Context, r Row) (string, error) { return describeRevision(r.Data.(azure.Revision)), nil },
		YAML:     func(_ context.Context, r Row) (any, error) { return r.Data.(azure.Revision).Raw, nil },
		Actions: []Action{
			act(tcell.KeyRune, 'l', "Logs", false, func(r Row) {
				rv := r.Data.(azure.Revision)
				a.withReplicas(rv.RG, rv.App, rv.Name, func(reps []azure.Replica) {
					var cts []azure.Container
					for _, rp := range reps {
						cts = append(cts, appContainers(rp)...)
					}
					if len(cts) == 0 {
						a.Flash("warn", "revision %s has no replicas", rv.Name)
						return
					}
					a.Push(newLogView(a, rv.Name, a.containerSources(cts)))
				})
			}),
			act(tcell.KeyRune, 'r', "Restart", true, func(r Row) {
				rv := r.Data.(azure.Revision)
				a.Confirm("Restart", "Restart revision "+rv.Name+"?", func() {
					a.Async("Restarting "+rv.Name, func(ctx context.Context) error { return a.az.RestartRevision(ctx, rv.RG, rv.App, rv.Name) })
				})
			}),
			act(tcell.KeyRune, 'a', "Activate/Deactivate", true, func(r Row) {
				rv := r.Data.(azure.Revision)
				verb := "Activate"
				if rv.Active {
					verb = "Deactivate"
				}
				a.Confirm(verb, verb+" revision "+rv.Name+"?", func() {
					a.Async(verb+" "+rv.Name, func(ctx context.Context) error {
						return a.az.SetRevisionActive(ctx, rv.RG, rv.App, rv.Name, !rv.Active)
					})
				})
			}),
		},
	})
}

// ---- Replicas -------------------------------------------------------------

func (a *App) replicasView(rg, app, rev string) Component {
	scope := app
	if rev != "" {
		scope = rev
	}
	return newTableView(a, &Resource{
		Title:   "Replicas",
		Scope:   scope,
		SortCol: 5,
		Columns: []Column{{Name: "NAME"}, {Name: "READY"}, {Name: "STATUS"}, {Name: "RESTARTS", Kind: ColNum},
			{Name: "REVISION"}, {Name: "AGE", Kind: ColAge}, {Name: "DETAILS"}},
		List: func(ctx context.Context) ([]Row, error) {
			var reps []azure.Replica
			var err error
			if rev == "" {
				reps, err = a.az.ListActiveReplicas(ctx, rg, app)
			} else {
				reps, err = a.az.ListReplicas(ctx, rg, app, rev)
			}
			if err != nil {
				return nil, err
			}
			rows := make([]Row, 0, len(reps))
			for _, r := range reps {
				status := r.State
				if status == "Running" {
					status = ""
				}
				for _, c := range r.Containers {
					if !c.Init && c.State != "" && c.State != "Running" {
						status = c.State + " " + c.Details
					}
				}
				rows = append(rows, Row{ID: r.Name, Status: status, Data: r, Cells: []string{
					r.Name, r.ReadyCount(), r.State, strconv.Itoa(int(r.Restarts())), r.Revision, ts(r.Created), r.Details}})
			}
			return rows, nil
		},
		EnterTip: "Containers",
		Enter:    func(r Row) { a.Push(a.containersView(r.Data.(azure.Replica))) },
		Describe: func(_ context.Context, r Row) (string, error) { return describeReplica(r.Data.(azure.Replica)), nil },
		YAML:     func(_ context.Context, r Row) (any, error) { return r.Data.(azure.Replica).Raw, nil },
		Actions: []Action{
			act(tcell.KeyRune, 'l', "Logs", false, func(r Row) {
				rp := r.Data.(azure.Replica)
				a.Push(newLogView(a, shortReplica(rp.Name), a.containerSources(appContainers(rp))))
			}),
			act(tcell.KeyRune, 's', "Shell", false, func(r Row) {
				a.pickContainer(appContainers(r.Data.(azure.Replica)), false, a.shell)
			}),
			act(tcell.KeyCtrlK, 0, "Restart Revision", true, func(r Row) {
				rp := r.Data.(azure.Replica)
				a.Confirm("Restart", "Replicas cannot be killed individually.\nRestart revision "+rp.Revision+"?", func() {
					a.Async("Restarting "+rp.Revision, func(ctx context.Context) error {
						return a.az.RestartRevision(ctx, rp.RG, rp.App, rp.Revision)
					})
				})
			}),
		},
	})
}

// ---- Containers -----------------------------------------------------------

func (a *App) containersView(rp azure.Replica) Component {
	return newTableView(a, &Resource{
		Title: "Containers",
		Scope: shortReplica(rp.Name),
		Columns: []Column{{Name: "NAME"}, {Name: "INIT"}, {Name: "READY"}, {Name: "STARTED"}, {Name: "STATE"},
			{Name: "RESTARTS", Kind: ColNum}, {Name: "DETAILS"}},
		List: func(context.Context) ([]Row, error) {
			rows := make([]Row, 0, len(rp.Containers))
			for _, c := range rp.Containers {
				status := c.State
				if status == "Running" || (c.Init && status == "Terminated") {
					status = ""
				}
				rows = append(rows, Row{ID: c.Name, Status: status, Data: c, Cells: []string{
					c.Name, onOff(c.Init), onOff(c.Ready), onOff(c.Started), c.State, strconv.Itoa(int(c.Restarts)), c.Details}})
			}
			return rows, nil
		},
		EnterTip: "Logs",
		Enter: func(r Row) {
			c := r.Data.(azure.Container)
			a.Push(newLogView(a, c.Name, a.containerSources([]azure.Container{c})))
		},
		Describe: func(_ context.Context, r Row) (string, error) {
			return describeContainer(r.Data.(azure.Container)), nil
		},
		Actions: []Action{
			act(tcell.KeyRune, 'l', "Logs", false, func(r Row) {
				c := r.Data.(azure.Container)
				a.Push(newLogView(a, c.Name, a.containerSources([]azure.Container{c})))
			}),
			act(tcell.KeyRune, 's', "Shell", false, func(r Row) { a.shell(r.Data.(azure.Container)) }),
		},
	})
}

// ---- Jobs -----------------------------------------------------------------

func (a *App) jobsView() Component {
	scope := a.Scope()
	return newTableView(a, &Resource{
		Title:  "Jobs",
		Scope:  scopeLabel(Scope{RG: scope.RG}),
		RGKeys: true,
		Columns: []Column{{Name: "NAME"}, {Name: "RG"}, {Name: "ENV"}, {Name: "TRIGGER"}, {Name: "SCHEDULE"},
			{Name: "STATE"}, {Name: "IMAGE"}, {Name: "AGE", Kind: ColAge}},
		List: func(ctx context.Context) ([]Row, error) {
			jobs, err := a.az.ListJobs(ctx, scope.RG)
			if err != nil {
				return nil, err
			}
			rows := make([]Row, 0, len(jobs))
			for _, j := range jobs {
				rows = append(rows, Row{ID: j.RG + "/" + j.Name, Status: okStatus(j.State, "Succeeded"), Data: j, Cells: []string{
					j.Name, j.RG, j.Env, j.Trigger, j.Schedule, j.State, j.Image, ts(j.Created)}})
			}
			return rows, nil
		},
		EnterTip: "Executions",
		Enter: func(r Row) {
			j := r.Data.(azure.Job)
			a.Push(a.executionsView(j.RG, j.Name))
		},
		Describe: func(ctx context.Context, r Row) (string, error) {
			j := r.Data.(azure.Job)
			raw, err := a.az.GetJob(ctx, j.RG, j.Name)
			if err != nil {
				return "", err
			}
			return describeJob(raw), nil
		},
		YAML: func(ctx context.Context, r Row) (any, error) {
			j := r.Data.(azure.Job)
			return a.az.GetJob(ctx, j.RG, j.Name)
		},
		Actions: []Action{
			act(tcell.KeyRune, 't', "Trigger", true, func(r Row) {
				j := r.Data.(azure.Job)
				a.Confirm("Trigger", "Start a new execution of job "+j.Name+"?", func() {
					a.Async("Triggering "+j.Name, func(ctx context.Context) error {
						_, err := a.az.StartJob(ctx, j.RG, j.Name)
						return err
					})
				})
			}),
			act(tcell.KeyRune, 'e', "Edit", true, func(r Row) { a.editJob(r.Data.(azure.Job)) }),
			act(tcell.KeyCtrlD, 0, "Delete", true, func(r Row) {
				j := r.Data.(azure.Job)
				a.Confirm("Delete", fmt.Sprintf("Delete job %s/%s?", j.RG, j.Name), func() {
					a.Async("Deleting "+j.Name, func(ctx context.Context) error { return a.az.DeleteJob(ctx, j.RG, j.Name) })
				})
			}),
		},
	})
}

func (a *App) executionsView(rg, job string) Component {
	return newTableView(a, &Resource{
		Title:   "Executions",
		Scope:   job,
		SortCol: 2,
		Columns: []Column{{Name: "NAME"}, {Name: "STATUS"}, {Name: "STARTED", Kind: ColAge}, {Name: "DURATION"}},
		List: func(ctx context.Context) ([]Row, error) {
			execs, err := a.az.ListExecutions(ctx, rg, job)
			if err != nil {
				return nil, err
			}
			rows := make([]Row, 0, len(execs))
			for _, e := range execs {
				dur := ""
				if !e.Start.IsZero() && !e.End.IsZero() {
					dur = humanDuration(e.End.Sub(e.Start))
				}
				status := e.Status
				switch status {
				case "Running":
					status = ""
				case "Succeeded":
					status = "completed"
				}
				rows = append(rows, Row{ID: e.Name, Status: status, Data: e, Cells: []string{e.Name, e.Status, ts(e.Start), dur}})
			}
			return rows, nil
		},
		YAML: func(_ context.Context, r Row) (any, error) { return r.Data.(azure.Execution).Raw, nil },
		Actions: []Action{
			act(tcell.KeyCtrlK, 0, "Stop", true, func(r Row) {
				e := r.Data.(azure.Execution)
				a.Confirm("Stop", "Stop execution "+e.Name+"?", func() {
					a.Async("Stopping "+e.Name, func(ctx context.Context) error { return a.az.StopExecution(ctx, e.RG, e.Job, e.Name) })
				})
			}),
		},
	})
}

// okStatus returns "" when state equals the healthy value, else the state.
func okStatus(state, healthy string) string {
	if strings.EqualFold(state, healthy) {
		return ""
	}
	return state
}
