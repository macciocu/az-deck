package azure

import (
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

// Subscription is an Azure subscription.
type Subscription struct {
	ID, Name, State, TenantID string
	Raw                       any
}

// ResourceGroup is an Azure resource group.
type ResourceGroup struct {
	Name, Location, State string
	Raw                   any
}

// Environment is a Container Apps managed environment.
type Environment struct {
	Name, RG, Location, State, Domain, StaticIP string
	EventStream                                 string
	Created                                     time.Time
	Raw                                         any
}

// App is a container app.
type App struct {
	Name, RG, Env, Location        string
	State, Running, FQDN, Revision string
	Mode, Image                    string
	MinReplicas, MaxReplicas       int32
	EventStream                    string
	Created                        time.Time
	Raw                            *armappcontainers.ContainerApp
}

// Revision is a container app revision.
type Revision struct {
	Name, RG, App                 string
	Active                        bool
	Replicas, Traffic             int32
	Health, Provisioning, Running string
	Created                       time.Time
	Raw                           any
}

// Replica is a running instance of a revision.
type Replica struct {
	Name, RG, App, Revision string
	State, Details          string
	Containers              []Container
	Created                 time.Time
	Raw                     any
}

// Container is a container inside a replica.
type Container struct {
	Name, Replica, Revision, RG, App string
	Ready, Started, Init             bool
	Restarts                         int32
	State, Details                   string
	LogStream, Exec                  string
}

// Job is a container apps job.
type Job struct {
	Name, RG, Env, Location, Trigger, Schedule, State, Image string
	Created                                                  time.Time
	Raw                                                      *armappcontainers.Job
}

// Execution is a job execution.
type Execution struct {
	Name, RG, Job, Status string
	Start, End            time.Time
	Raw                   any
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func i32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func boolv(p *bool) bool { return p != nil && *p }

func tm(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

// rgFromID extracts the resource group name from an ARM resource ID.
func rgFromID(id string) string {
	parts := strings.Split(id, "/")
	for i := 0; i < len(parts)-1; i++ {
		if strings.EqualFold(parts[i], "resourceGroups") {
			return parts[i+1]
		}
	}
	return ""
}

// lastSegment returns the final path element of an ARM ID.
func lastSegment(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 {
		return id[i+1:]
	}
	return id
}

func newSubscription(s *armsubscriptions.Subscription) Subscription {
	out := Subscription{ID: str(s.SubscriptionID), Name: str(s.DisplayName), TenantID: str(s.TenantID), Raw: s}
	if s.State != nil {
		out.State = string(*s.State)
	}
	return out
}

func newResourceGroup(g *armresources.ResourceGroup) ResourceGroup {
	out := ResourceGroup{Name: str(g.Name), Location: str(g.Location), Raw: g}
	if g.Properties != nil {
		out.State = str(g.Properties.ProvisioningState)
	}
	return out
}

func newEnvironment(e *armappcontainers.ManagedEnvironment) Environment {
	out := Environment{Name: str(e.Name), RG: rgFromID(str(e.ID)), Location: str(e.Location), Raw: e}
	if e.SystemData != nil {
		out.Created = tm(e.SystemData.CreatedAt)
	}
	if p := e.Properties; p != nil {
		if p.ProvisioningState != nil {
			out.State = string(*p.ProvisioningState)
		}
		out.Domain = str(p.DefaultDomain)
		out.StaticIP = str(p.StaticIP)
		out.EventStream = str(p.EventStreamEndpoint)
	}
	return out
}

func newApp(a *armappcontainers.ContainerApp) App {
	out := App{Name: str(a.Name), RG: rgFromID(str(a.ID)), Location: str(a.Location), Raw: a}
	if a.SystemData != nil {
		out.Created = tm(a.SystemData.CreatedAt)
	}
	p := a.Properties
	if p == nil {
		return out
	}
	envID := str(p.ManagedEnvironmentID)
	if envID == "" {
		envID = str(p.EnvironmentID)
	}
	out.Env = lastSegment(envID)
	if p.ProvisioningState != nil {
		out.State = string(*p.ProvisioningState)
	}
	if p.RunningStatus != nil {
		out.Running = string(*p.RunningStatus)
	}
	out.Revision = str(p.LatestRevisionName)
	out.EventStream = str(p.EventStreamEndpoint)
	if c := p.Configuration; c != nil {
		if c.ActiveRevisionsMode != nil {
			out.Mode = string(*c.ActiveRevisionsMode)
		}
		if c.Ingress != nil {
			out.FQDN = str(c.Ingress.Fqdn)
		}
	}
	if t := p.Template; t != nil {
		if len(t.Containers) > 0 {
			out.Image = str(t.Containers[0].Image)
		}
		if t.Scale != nil {
			out.MinReplicas = i32(t.Scale.MinReplicas)
			out.MaxReplicas = i32(t.Scale.MaxReplicas)
		}
	}
	return out
}

func newRevision(r *armappcontainers.Revision, rg, app string) Revision {
	out := Revision{Name: str(r.Name), RG: rg, App: app, Raw: r}
	if p := r.Properties; p != nil {
		out.Active = boolv(p.Active)
		out.Replicas = i32(p.Replicas)
		out.Traffic = i32(p.TrafficWeight)
		out.Created = tm(p.CreatedTime)
		if p.HealthState != nil {
			out.Health = string(*p.HealthState)
		}
		if p.ProvisioningState != nil {
			out.Provisioning = string(*p.ProvisioningState)
		}
		if p.RunningState != nil {
			out.Running = string(*p.RunningState)
		}
	}
	return out
}

func newReplica(r *armappcontainers.Replica, rg, app, rev string) Replica {
	out := Replica{Name: str(r.Name), RG: rg, App: app, Revision: rev, Raw: r}
	p := r.Properties
	if p == nil {
		return out
	}
	out.Created = tm(p.CreatedTime)
	if p.RunningState != nil {
		out.State = string(*p.RunningState)
	}
	out.Details = str(p.RunningStateDetails)
	add := func(cs []*armappcontainers.ReplicaContainer, init bool) {
		for _, c := range cs {
			ct := Container{
				Name: str(c.Name), Replica: out.Name, Revision: rev, RG: rg, App: app,
				Ready: boolv(c.Ready), Started: boolv(c.Started), Init: init,
				Restarts: i32(c.RestartCount), Details: str(c.RunningStateDetails),
				LogStream: str(c.LogStreamEndpoint), Exec: str(c.ExecEndpoint),
			}
			if c.RunningState != nil {
				ct.State = string(*c.RunningState)
			}
			out.Containers = append(out.Containers, ct)
		}
	}
	add(p.InitContainers, true)
	add(p.Containers, false)
	return out
}

// ReadyCount returns "ready/total" for non-init containers.
func (r Replica) ReadyCount() string {
	ready, total := 0, 0
	for _, c := range r.Containers {
		if c.Init {
			continue
		}
		total++
		if c.Ready {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d", ready, total)
}

// Restarts sums container restarts.
func (r Replica) Restarts() int32 {
	var n int32
	for _, c := range r.Containers {
		n += c.Restarts
	}
	return n
}

func newJob(j *armappcontainers.Job) Job {
	out := Job{Name: str(j.Name), RG: rgFromID(str(j.ID)), Location: str(j.Location), Raw: j}
	if j.SystemData != nil {
		out.Created = tm(j.SystemData.CreatedAt)
	}
	p := j.Properties
	if p == nil {
		return out
	}
	out.Env = lastSegment(str(p.EnvironmentID))
	if p.ProvisioningState != nil {
		out.State = string(*p.ProvisioningState)
	}
	if c := p.Configuration; c != nil {
		if c.TriggerType != nil {
			out.Trigger = string(*c.TriggerType)
		}
		if c.ScheduleTriggerConfig != nil {
			out.Schedule = str(c.ScheduleTriggerConfig.CronExpression)
		}
	}
	if t := p.Template; t != nil && len(t.Containers) > 0 {
		out.Image = str(t.Containers[0].Image)
	}
	return out
}

func newExecution(e *armappcontainers.JobExecution, rg, job string) Execution {
	out := Execution{Name: str(e.Name), RG: rg, Job: job, Raw: e}
	if p := e.Properties; p != nil {
		out.Start = tm(p.StartTime)
		out.End = tm(p.EndTime)
		if p.Status != nil {
			out.Status = string(*p.Status)
		}
	}
	return out
}
