// Package azure wraps the Azure SDK calls azd needs to browse and operate
// Azure Container Apps.
package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

// Client is a subscription-scoped Azure client. Switching subscription swaps
// the underlying SDK clients.
type Client struct {
	cred azcore.TokenCredential

	mu    sync.RWMutex
	sub   Subscription
	apps  *armappcontainers.ContainerAppsClient
	revs  *armappcontainers.ContainerAppsRevisionsClient
	reps  *armappcontainers.ContainerAppsRevisionReplicasClient
	envs  *armappcontainers.ManagedEnvironmentsClient
	jobs  *armappcontainers.JobsClient
	execs *armappcontainers.JobsExecutionsClient
	rgs   *armresources.ResourceGroupsClient
	subs  *armsubscriptions.Client
}

// NewClient authenticates using the default Azure credential chain
// (env vars, workload/managed identity, Azure CLI, Azure Developer CLI).
func NewClient() (*Client, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure credentials: %w", err)
	}
	subs, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return nil, err
	}
	return &Client{cred: cred, subs: subs}, nil
}

// Subscription returns the active subscription.
func (c *Client) Subscription() Subscription {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sub
}

// UseSubscription points every resource client at the given subscription.
func (c *Client) UseSubscription(s Subscription) error {
	id := s.ID
	apps, err := armappcontainers.NewContainerAppsClient(id, c.cred, nil)
	if err != nil {
		return err
	}
	revs, err := armappcontainers.NewContainerAppsRevisionsClient(id, c.cred, nil)
	if err != nil {
		return err
	}
	reps, err := armappcontainers.NewContainerAppsRevisionReplicasClient(id, c.cred, nil)
	if err != nil {
		return err
	}
	envs, err := armappcontainers.NewManagedEnvironmentsClient(id, c.cred, nil)
	if err != nil {
		return err
	}
	jobs, err := armappcontainers.NewJobsClient(id, c.cred, nil)
	if err != nil {
		return err
	}
	execs, err := armappcontainers.NewJobsExecutionsClient(id, c.cred, nil)
	if err != nil {
		return err
	}
	rgs, err := armresources.NewResourceGroupsClient(id, c.cred, nil)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sub, c.apps, c.revs, c.reps, c.envs, c.jobs, c.execs, c.rgs = s, apps, revs, reps, envs, jobs, execs, rgs
	return nil
}

// ResolveSubscription picks the subscription to start with: an explicit
// name/ID, then $AZURE_SUBSCRIPTION_ID, then the Azure CLI default, then the
// first enabled subscription.
func (c *Client) ResolveSubscription(ctx context.Context, want string) (Subscription, error) {
	subs, err := c.ListSubscriptions(ctx)
	if err != nil {
		return Subscription{}, err
	}
	if len(subs) == 0 {
		return Subscription{}, errors.New("no Azure subscriptions available for these credentials")
	}
	candidates := []string{want, os.Getenv("AZURE_SUBSCRIPTION_ID"), cliDefaultSubscription()}
	for _, w := range candidates {
		if w == "" {
			continue
		}
		for _, s := range subs {
			if strings.EqualFold(s.ID, w) || strings.EqualFold(s.Name, w) {
				return s, nil
			}
		}
		if w == want {
			return Subscription{}, fmt.Errorf("subscription %q not found", want)
		}
	}
	for _, s := range subs {
		if s.State == "Enabled" {
			return s, nil
		}
	}
	return subs[0], nil
}

// cliDefaultSubscription reads the default subscription from the Azure CLI
// profile, if present.
func cliDefaultSubscription() string {
	dir := os.Getenv("AZURE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".azure")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "azureProfile.json"))
	if err != nil {
		return ""
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	var profile struct {
		Subscriptions []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"isDefault"`
		} `json:"subscriptions"`
	}
	if json.Unmarshal(raw, &profile) != nil {
		return ""
	}
	for _, s := range profile.Subscriptions {
		if s.IsDefault {
			return s.ID
		}
	}
	return ""
}

// ListSubscriptions lists all subscriptions visible to the credential.
func (c *Client) ListSubscriptions(ctx context.Context) ([]Subscription, error) {
	var out []Subscription
	pager := c.subs.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range page.Value {
			out = append(out, newSubscription(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// ListResourceGroups lists resource groups in the active subscription.
func (c *Client) ListResourceGroups(ctx context.Context) ([]ResourceGroup, error) {
	c.mu.RLock()
	rgs := c.rgs
	c.mu.RUnlock()
	var out []ResourceGroup
	pager := rgs.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, g := range page.Value {
			out = append(out, newResourceGroup(g))
		}
	}
	return out, nil
}

// ListEnvironments lists managed environments, optionally scoped to a resource group.
func (c *Client) ListEnvironments(ctx context.Context, rg string) ([]Environment, error) {
	c.mu.RLock()
	cl := c.envs
	c.mu.RUnlock()
	var out []Environment
	if rg != "" {
		pager := cl.NewListByResourceGroupPager(rg, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, e := range page.Value {
				out = append(out, newEnvironment(e))
			}
		}
		return out, nil
	}
	pager := cl.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, e := range page.Value {
			out = append(out, newEnvironment(e))
		}
	}
	return out, nil
}

// ListApps lists container apps, optionally scoped to a resource group and/or environment name.
func (c *Client) ListApps(ctx context.Context, rg, env string) ([]App, error) {
	c.mu.RLock()
	cl := c.apps
	c.mu.RUnlock()
	var raw []*armappcontainers.ContainerApp
	if rg != "" {
		pager := cl.NewListByResourceGroupPager(rg, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			raw = append(raw, page.Value...)
		}
	} else {
		pager := cl.NewListBySubscriptionPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			raw = append(raw, page.Value...)
		}
	}
	out := make([]App, 0, len(raw))
	for _, a := range raw {
		app := newApp(a)
		if env != "" && !strings.EqualFold(app.Env, env) {
			continue
		}
		out = append(out, app)
	}
	return out, nil
}

// GetApp fetches a single container app.
func (c *Client) GetApp(ctx context.Context, rg, name string) (*armappcontainers.ContainerApp, error) {
	c.mu.RLock()
	cl := c.apps
	c.mu.RUnlock()
	res, err := cl.Get(ctx, rg, name, nil)
	if err != nil {
		return nil, err
	}
	return &res.ContainerApp, nil
}

// UpdateApp PATCHes a container app (JSON merge patch) and waits for completion.
func (c *Client) UpdateApp(ctx context.Context, rg, name string, patch armappcontainers.ContainerApp) error {
	c.mu.RLock()
	cl := c.apps
	c.mu.RUnlock()
	poller, err := cl.BeginUpdate(ctx, rg, name, patch, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// ScaleApp sets min/max replicas on the app template (creates a new revision
// in single-revision mode).
func (c *Client) ScaleApp(ctx context.Context, rg, name string, minR, maxR int32) error {
	app, err := c.GetApp(ctx, rg, name)
	if err != nil {
		return err
	}
	if app.Properties == nil || app.Properties.Template == nil {
		return errors.New("app has no template")
	}
	tpl := app.Properties.Template
	if tpl.Scale == nil {
		tpl.Scale = &armappcontainers.Scale{}
	}
	tpl.Scale.MinReplicas = &minR
	tpl.Scale.MaxReplicas = &maxR
	return c.UpdateApp(ctx, rg, name, armappcontainers.ContainerApp{
		Location:   app.Location,
		Properties: &armappcontainers.ContainerAppProperties{Template: tpl},
	})
}

// DeleteApp deletes a container app.
func (c *Client) DeleteApp(ctx context.Context, rg, name string) error {
	c.mu.RLock()
	cl := c.apps
	c.mu.RUnlock()
	poller, err := cl.BeginDelete(ctx, rg, name, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// StartApp starts a stopped container app.
func (c *Client) StartApp(ctx context.Context, rg, name string) error {
	c.mu.RLock()
	cl := c.apps
	c.mu.RUnlock()
	poller, err := cl.BeginStart(ctx, rg, name, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// StopApp stops a running container app.
func (c *Client) StopApp(ctx context.Context, rg, name string) error {
	c.mu.RLock()
	cl := c.apps
	c.mu.RUnlock()
	poller, err := cl.BeginStop(ctx, rg, name, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// RestartApp restarts every active revision of an app.
func (c *Client) RestartApp(ctx context.Context, rg, name string) error {
	revs, err := c.ListRevisions(ctx, rg, name)
	if err != nil {
		return err
	}
	n := 0
	for _, r := range revs {
		if !r.Active {
			continue
		}
		if err := c.RestartRevision(ctx, rg, name, r.Name); err != nil {
			return err
		}
		n++
	}
	if n == 0 {
		return errors.New("no active revisions to restart")
	}
	return nil
}

// AuthToken returns a short-lived token for the app's log stream / exec endpoints.
func (c *Client) AuthToken(ctx context.Context, rg, app string) (string, error) {
	c.mu.RLock()
	cl := c.apps
	c.mu.RUnlock()
	res, err := cl.GetAuthToken(ctx, rg, app, nil)
	if err != nil {
		return "", err
	}
	if res.Properties == nil || res.Properties.Token == nil {
		return "", errors.New("empty auth token")
	}
	return *res.Properties.Token, nil
}

// EnvAuthToken returns a token for an environment's event stream.
func (c *Client) EnvAuthToken(ctx context.Context, rg, env string) (string, error) {
	c.mu.RLock()
	cl := c.envs
	c.mu.RUnlock()
	res, err := cl.GetAuthToken(ctx, rg, env, nil)
	if err != nil {
		return "", err
	}
	if res.Properties == nil || res.Properties.Token == nil {
		return "", errors.New("empty auth token")
	}
	return *res.Properties.Token, nil
}

// DeleteEnvironment deletes a managed environment.
func (c *Client) DeleteEnvironment(ctx context.Context, rg, name string) error {
	c.mu.RLock()
	cl := c.envs
	c.mu.RUnlock()
	poller, err := cl.BeginDelete(ctx, rg, name, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// ListRevisions lists the revisions of an app, newest first.
func (c *Client) ListRevisions(ctx context.Context, rg, app string) ([]Revision, error) {
	c.mu.RLock()
	cl := c.revs
	c.mu.RUnlock()
	var out []Revision
	pager := cl.NewListRevisionsPager(rg, app, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range page.Value {
			out = append(out, newRevision(r, rg, app))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// RestartRevision restarts all replicas of a revision.
func (c *Client) RestartRevision(ctx context.Context, rg, app, rev string) error {
	c.mu.RLock()
	cl := c.revs
	c.mu.RUnlock()
	_, err := cl.RestartRevision(ctx, rg, app, rev, nil)
	return err
}

// SetRevisionActive activates or deactivates a revision.
func (c *Client) SetRevisionActive(ctx context.Context, rg, app, rev string, active bool) error {
	c.mu.RLock()
	cl := c.revs
	c.mu.RUnlock()
	var err error
	if active {
		_, err = cl.ActivateRevision(ctx, rg, app, rev, nil)
	} else {
		_, err = cl.DeactivateRevision(ctx, rg, app, rev, nil)
	}
	return err
}

// ListReplicas lists the replicas of a revision.
func (c *Client) ListReplicas(ctx context.Context, rg, app, rev string) ([]Replica, error) {
	c.mu.RLock()
	cl := c.reps
	c.mu.RUnlock()
	res, err := cl.ListReplicas(ctx, rg, app, rev, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Replica, 0, len(res.Value))
	for _, r := range res.Value {
		out = append(out, newReplica(r, rg, app, rev))
	}
	return out, nil
}

// ListActiveReplicas lists replicas across all active revisions of an app.
func (c *Client) ListActiveReplicas(ctx context.Context, rg, app string) ([]Replica, error) {
	revs, err := c.ListRevisions(ctx, rg, app)
	if err != nil {
		return nil, err
	}
	var out []Replica
	for _, r := range revs {
		if !r.Active {
			continue
		}
		reps, err := c.ListReplicas(ctx, rg, app, r.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, reps...)
	}
	return out, nil
}

// ListJobs lists container app jobs, optionally scoped to a resource group.
func (c *Client) ListJobs(ctx context.Context, rg string) ([]Job, error) {
	c.mu.RLock()
	cl := c.jobs
	c.mu.RUnlock()
	var out []Job
	if rg != "" {
		pager := cl.NewListByResourceGroupPager(rg, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, j := range page.Value {
				out = append(out, newJob(j))
			}
		}
		return out, nil
	}
	pager := cl.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, j := range page.Value {
			out = append(out, newJob(j))
		}
	}
	return out, nil
}

// GetJob fetches a single job.
func (c *Client) GetJob(ctx context.Context, rg, name string) (*armappcontainers.Job, error) {
	c.mu.RLock()
	cl := c.jobs
	c.mu.RUnlock()
	res, err := cl.Get(ctx, rg, name, nil)
	if err != nil {
		return nil, err
	}
	return &res.Job, nil
}

// UpdateJob PATCHes a job and waits for completion.
func (c *Client) UpdateJob(ctx context.Context, rg, name string, patch armappcontainers.JobPatchProperties) error {
	c.mu.RLock()
	cl := c.jobs
	c.mu.RUnlock()
	poller, err := cl.BeginUpdate(ctx, rg, name, patch, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// StartJob triggers a new job execution and returns its name.
func (c *Client) StartJob(ctx context.Context, rg, name string) (string, error) {
	c.mu.RLock()
	cl := c.jobs
	c.mu.RUnlock()
	poller, err := cl.BeginStart(ctx, rg, name, nil)
	if err != nil {
		return "", err
	}
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return "", err
	}
	if res.Name != nil {
		return *res.Name, nil
	}
	return "", nil
}

// DeleteJob deletes a job.
func (c *Client) DeleteJob(ctx context.Context, rg, name string) error {
	c.mu.RLock()
	cl := c.jobs
	c.mu.RUnlock()
	poller, err := cl.BeginDelete(ctx, rg, name, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// ListExecutions lists executions of a job, newest first.
func (c *Client) ListExecutions(ctx context.Context, rg, job string) ([]Execution, error) {
	c.mu.RLock()
	cl := c.execs
	c.mu.RUnlock()
	var out []Execution
	pager := cl.NewListPager(rg, job, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, e := range page.Value {
			out = append(out, newExecution(e, rg, job))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	return out, nil
}

// StopExecution stops a running job execution.
func (c *Client) StopExecution(ctx context.Context, rg, job, exec string) error {
	c.mu.RLock()
	cl := c.jobs
	c.mu.RUnlock()
	poller, err := cl.BeginStopExecution(ctx, rg, job, exec, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}
