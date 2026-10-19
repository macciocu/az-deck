// Command azd is a k9s-style terminal UI for Azure Container Apps.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/macciocu/az-deck/internal/azure"
	"github.com/macciocu/az-deck/internal/config"
	"github.com/macciocu/az-deck/internal/ui"
)

// Set via -ldflags at build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	var (
		sub      = flag.String("subscription", "", "subscription name or ID (default: last used, $AZURE_SUBSCRIPTION_ID, or az CLI default)")
		rg       = flag.String("resource-group", "", "resource group to scope views to")
		command  = flag.String("command", "", "view to start in, e.g. apps, jobs, env, rg, sub")
		refresh  = flag.Duration("refresh", 5*time.Second, "table refresh interval")
		shell    = flag.String("shell", "sh", "command used for interactive shells")
		readOnly = flag.Bool("readonly", false, "disable all mutating actions")
		showVer  = flag.Bool("version", false, "print version and exit")
	)
	flag.StringVar(sub, "s", "", "shorthand for -subscription")
	flag.StringVar(rg, "g", "", "shorthand for -resource-group")
	flag.StringVar(command, "c", "", "shorthand for -command")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "azd - a k9s-style terminal UI for Azure Container Apps\n\nUsage: azd [flags]\n\nFlags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nAuthentication uses the Azure default credential chain; run `az login` first.\n")
	}
	flag.Parse()

	if *showVer {
		fmt.Printf("azd %s (commit %s, built %s)\n", version, commit, date)
		return
	}
	if err := run(*sub, *rg, *command, *refresh, *shell, *readOnly); err != nil {
		fmt.Fprintln(os.Stderr, "azd:", err)
		os.Exit(1)
	}
}

func run(sub, rg, command string, refresh time.Duration, shell string, readOnly bool) error {
	state := config.Load()
	client, err := azure.NewClient()
	if err != nil {
		return err
	}

	fmt.Fprint(os.Stderr, "azd: connecting to Azure…\r")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	want := sub
	if want == "" && os.Getenv("AZURE_SUBSCRIPTION_ID") == "" {
		want = state.Subscription
	}
	s, err := client.ResolveSubscription(ctx, want)
	if err != nil && want != "" && sub == "" {
		// The remembered subscription may no longer exist; fall back to defaults.
		s, err = client.ResolveSubscription(ctx, "")
	}
	if err != nil {
		return fmt.Errorf("%s\n(hint: run `az login`)", azure.ErrorMessage(err))
	}
	if err := client.UseSubscription(s); err != nil {
		return err
	}
	if s.ID != state.Subscription {
		state.Subscription = s.ID
		state.RG = "" // resource groups are per subscription
	}
	if rg == "" {
		rg = state.RG
	}
	_ = state.Save()

	app := ui.New(client, state, ui.Options{
		Version:       version,
		Subscription:  s.ID,
		ResourceGroup: rg,
		Command:       command,
		Refresh:       refresh,
		Shell:         shell,
		ReadOnly:      readOnly,
	})
	return app.Run()
}
