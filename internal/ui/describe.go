package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"

	"github.com/macciocu/az-deck/internal/azure"
)

// desc builds kubectl-describe style text.
type desc struct{ b strings.Builder }

func (d *desc) kv(indent int, k string, v any) {
	s := fmt.Sprint(v)
	switch x := v.(type) {
	case *string:
		s = deref(x)
	case *int32:
		if x == nil {
			s = ""
		} else {
			s = fmt.Sprint(*x)
		}
	case *bool:
		s = fmt.Sprint(x != nil && *x)
	case time.Time:
		if x.IsZero() {
			s = "<unknown>"
		} else {
			s = x.Local().Format(time.RFC1123) + " (" + humanDuration(time.Since(x)) + " ago)"
		}
	}
	if s == "" {
		s = "<none>"
	}
	fmt.Fprintf(&d.b, "%s%-*s %s\n", strings.Repeat("  ", indent), 22-2*indent, k+":", s)
}

func (d *desc) section(indent int, k string) {
	fmt.Fprintf(&d.b, "%s%s:\n", strings.Repeat("  ", indent), k)
}

func (d *desc) String() string { return d.b.String() }

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func strEnum[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func describeContainers(d *desc, indent int, cs []*armappcontainers.Container) {
	for _, c := range cs {
		d.section(indent, deref(c.Name))
		d.kv(indent+1, "Image", c.Image)
		if len(c.Command) > 0 {
			d.kv(indent+1, "Command", joinPtrs(c.Command))
		}
		if len(c.Args) > 0 {
			d.kv(indent+1, "Args", joinPtrs(c.Args))
		}
		if r := c.Resources; r != nil {
			cpu := ""
			if r.CPU != nil {
				cpu = fmt.Sprintf("%g", *r.CPU)
			}
			d.kv(indent+1, "CPU", cpu)
			d.kv(indent+1, "Memory", r.Memory)
			if r.EphemeralStorage != nil {
				d.kv(indent+1, "Ephemeral Storage", r.EphemeralStorage)
			}
		}
		if len(c.Probes) > 0 {
			d.section(indent+1, "Probes")
			for _, p := range c.Probes {
				target := ""
				switch {
				case p.HTTPGet != nil:
					target = fmt.Sprintf("http-get %s:%d", deref(p.HTTPGet.Path), deref32(p.HTTPGet.Port))
				case p.TCPSocket != nil:
					target = fmt.Sprintf("tcp-socket :%d", deref32(p.TCPSocket.Port))
				}
				d.kv(indent+2, strEnum(p.Type), fmt.Sprintf("%s delay=%ds period=%ds timeout=%ds failure=%d",
					target, deref32(p.InitialDelaySeconds), deref32(p.PeriodSeconds), deref32(p.TimeoutSeconds), deref32(p.FailureThreshold)))
			}
		}
		if len(c.Env) > 0 {
			d.section(indent+1, "Environment")
			for _, e := range c.Env {
				v := deref(e.Value)
				if e.SecretRef != nil {
					v = "<secret:" + *e.SecretRef + ">"
				}
				d.kv(indent+2, deref(e.Name), v)
			}
		}
		if len(c.VolumeMounts) > 0 {
			d.section(indent+1, "Mounts")
			for _, m := range c.VolumeMounts {
				d.kv(indent+2, deref(m.VolumeName), deref(m.MountPath))
			}
		}
	}
}

func deref32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func joinPtrs(ss []*string) string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, deref(s))
	}
	return strings.Join(out, " ")
}

func describeApp(ap *armappcontainers.ContainerApp) string {
	var d desc
	d.kv(0, "Name", ap.Name)
	d.kv(0, "Location", ap.Location)
	d.kv(0, "ID", ap.ID)
	if ap.SystemData != nil && ap.SystemData.CreatedAt != nil {
		d.kv(0, "Created", *ap.SystemData.CreatedAt)
	}
	if len(ap.Tags) > 0 {
		d.section(0, "Tags")
		for k, v := range ap.Tags {
			d.kv(1, k, v)
		}
	}
	p := ap.Properties
	if p == nil {
		return d.String()
	}
	d.kv(0, "Environment", p.ManagedEnvironmentID)
	d.kv(0, "Workload Profile", p.WorkloadProfileName)
	d.kv(0, "Provisioning", strEnum(p.ProvisioningState))
	d.kv(0, "Running Status", strEnum(p.RunningStatus))
	d.kv(0, "Latest Revision", p.LatestRevisionName)
	d.kv(0, "Latest Ready", p.LatestReadyRevisionName)
	d.kv(0, "Revision FQDN", p.LatestRevisionFqdn)
	if c := p.Configuration; c != nil {
		d.section(0, "Configuration")
		d.kv(1, "Revision Mode", strEnum(c.ActiveRevisionsMode))
		d.kv(1, "Max Inactive Revs", c.MaxInactiveRevisions)
		if in := c.Ingress; in != nil {
			d.section(1, "Ingress")
			d.kv(2, "FQDN", in.Fqdn)
			d.kv(2, "External", in.External)
			d.kv(2, "Target Port", in.TargetPort)
			d.kv(2, "Transport", strEnum(in.Transport))
			d.kv(2, "Allow Insecure", in.AllowInsecure)
			if len(in.Traffic) > 0 {
				d.section(2, "Traffic")
				for _, t := range in.Traffic {
					name := deref(t.RevisionName)
					if t.LatestRevision != nil && *t.LatestRevision {
						name = "latest"
					}
					if t.Label != nil {
						name += " (" + *t.Label + ")"
					}
					d.kv(3, name, fmt.Sprintf("%d%%", deref32(t.Weight)))
				}
			}
		}
		if len(c.Secrets) > 0 {
			names := make([]string, 0, len(c.Secrets))
			for _, s := range c.Secrets {
				names = append(names, deref(s.Name))
			}
			d.kv(1, "Secrets", strings.Join(names, ", "))
		}
		for _, r := range c.Registries {
			d.kv(1, "Registry", deref(r.Server))
		}
		if c.Dapr != nil && c.Dapr.Enabled != nil && *c.Dapr.Enabled {
			d.kv(1, "Dapr App ID", c.Dapr.AppID)
		}
	}
	if t := p.Template; t != nil {
		d.section(0, "Template")
		d.kv(1, "Revision Suffix", t.RevisionSuffix)
		if s := t.Scale; s != nil {
			d.section(1, "Scale")
			d.kv(2, "Min Replicas", s.MinReplicas)
			d.kv(2, "Max Replicas", s.MaxReplicas)
			for _, r := range s.Rules {
				kind := "custom"
				switch {
				case r.HTTP != nil:
					kind = "http"
				case r.TCP != nil:
					kind = "tcp"
				case r.AzureQueue != nil:
					kind = "azure-queue"
				case r.Custom != nil:
					kind = deref(r.Custom.Type)
				}
				d.kv(2, "Rule "+deref(r.Name), kind)
			}
		}
		if len(t.InitContainers) > 0 {
			d.section(1, "Init Containers")
			for _, ic := range t.InitContainers {
				d.section(2, deref(ic.Name))
				d.kv(3, "Image", ic.Image)
			}
		}
		d.section(1, "Containers")
		describeContainers(&d, 2, t.Containers)
	}
	if len(p.OutboundIPAddresses) > 0 {
		d.kv(0, "Outbound IPs", joinPtrs(p.OutboundIPAddresses))
	}
	return d.String()
}

func describeRevision(r azure.Revision) string {
	var d desc
	d.kv(0, "Name", r.Name)
	d.kv(0, "App", r.App)
	d.kv(0, "Resource Group", r.RG)
	d.kv(0, "Created", r.Created)
	d.kv(0, "Active", r.Active)
	d.kv(0, "Traffic", fmt.Sprintf("%d%%", r.Traffic))
	d.kv(0, "Replicas", r.Replicas)
	d.kv(0, "Health", r.Health)
	d.kv(0, "Running State", r.Running)
	d.kv(0, "Provisioning", r.Provisioning)
	if raw, ok := r.Raw.(*armappcontainers.Revision); ok && raw.Properties != nil {
		p := raw.Properties
		d.kv(0, "FQDN", p.Fqdn)
		if p.LastActiveTime != nil {
			d.kv(0, "Last Active", *p.LastActiveTime)
		}
		if p.ProvisioningError != nil {
			d.kv(0, "Provisioning Error", p.ProvisioningError)
		}
		if t := p.Template; t != nil {
			if s := t.Scale; s != nil {
				d.kv(0, "Scale", fmt.Sprintf("%d-%d", deref32(s.MinReplicas), deref32(s.MaxReplicas)))
			}
			d.section(0, "Containers")
			describeContainers(&d, 1, t.Containers)
		}
	}
	return d.String()
}

func describeReplica(r azure.Replica) string {
	var d desc
	d.kv(0, "Name", r.Name)
	d.kv(0, "App", r.App)
	d.kv(0, "Revision", r.Revision)
	d.kv(0, "Resource Group", r.RG)
	d.kv(0, "Created", r.Created)
	d.kv(0, "State", r.State)
	d.kv(0, "Details", r.Details)
	d.kv(0, "Ready", r.ReadyCount())
	d.section(0, "Containers")
	for _, c := range r.Containers {
		describeReplicaContainer(&d, 1, c)
	}
	return d.String()
}

func describeReplicaContainer(d *desc, indent int, c azure.Container) {
	d.section(indent, c.Name)
	d.kv(indent+1, "Init", c.Init)
	d.kv(indent+1, "State", c.State)
	d.kv(indent+1, "Details", c.Details)
	d.kv(indent+1, "Ready", c.Ready)
	d.kv(indent+1, "Started", c.Started)
	d.kv(indent+1, "Restart Count", c.Restarts)
}

func describeContainer(c azure.Container) string {
	var d desc
	d.kv(0, "Replica", c.Replica)
	d.kv(0, "Revision", c.Revision)
	d.kv(0, "App", c.App)
	describeReplicaContainer(&d, 0, c)
	d.kv(0, "Log Stream", c.LogStream)
	d.kv(0, "Exec", c.Exec)
	return d.String()
}

func describeEnv(e azure.Environment) string {
	var d desc
	d.kv(0, "Name", e.Name)
	d.kv(0, "Resource Group", e.RG)
	d.kv(0, "Location", e.Location)
	d.kv(0, "Created", e.Created)
	d.kv(0, "State", e.State)
	d.kv(0, "Default Domain", e.Domain)
	d.kv(0, "Static IP", e.StaticIP)
	if raw, ok := e.Raw.(*armappcontainers.ManagedEnvironment); ok && raw.Properties != nil {
		p := raw.Properties
		d.kv(0, "Zone Redundant", p.ZoneRedundant)
		d.kv(0, "Infra RG", p.InfrastructureResourceGroup)
		if p.VnetConfiguration != nil {
			d.section(0, "VNet")
			d.kv(1, "Internal", p.VnetConfiguration.Internal)
			d.kv(1, "Subnet", p.VnetConfiguration.InfrastructureSubnetID)
		}
		if p.AppLogsConfiguration != nil {
			d.kv(0, "Logs Destination", p.AppLogsConfiguration.Destination)
		}
		if len(p.WorkloadProfiles) > 0 {
			d.section(0, "Workload Profiles")
			for _, w := range p.WorkloadProfiles {
				d.kv(1, deref(w.Name), fmt.Sprintf("%s min=%d max=%d", deref(w.WorkloadProfileType), deref32(w.MinimumCount), deref32(w.MaximumCount)))
			}
		}
		if p.DeploymentErrors != nil {
			d.kv(0, "Deployment Errors", p.DeploymentErrors)
		}
	}
	return d.String()
}

func describeJob(j *armappcontainers.Job) string {
	var d desc
	d.kv(0, "Name", j.Name)
	d.kv(0, "Location", j.Location)
	d.kv(0, "ID", j.ID)
	p := j.Properties
	if p == nil {
		return d.String()
	}
	d.kv(0, "Environment", p.EnvironmentID)
	d.kv(0, "Provisioning", strEnum(p.ProvisioningState))
	if c := p.Configuration; c != nil {
		d.section(0, "Configuration")
		d.kv(1, "Trigger", strEnum(c.TriggerType))
		d.kv(1, "Replica Timeout", c.ReplicaTimeout)
		d.kv(1, "Retry Limit", c.ReplicaRetryLimit)
		if s := c.ScheduleTriggerConfig; s != nil {
			d.kv(1, "Cron", s.CronExpression)
			d.kv(1, "Parallelism", s.Parallelism)
			d.kv(1, "Completions", s.ReplicaCompletionCount)
		}
		if m := c.ManualTriggerConfig; m != nil {
			d.kv(1, "Parallelism", m.Parallelism)
			d.kv(1, "Completions", m.ReplicaCompletionCount)
		}
		if e := c.EventTriggerConfig; e != nil {
			d.kv(1, "Parallelism", e.Parallelism)
			d.kv(1, "Completions", e.ReplicaCompletionCount)
		}
	}
	if t := p.Template; t != nil {
		d.section(0, "Containers")
		describeContainers(&d, 1, t.Containers)
	}
	return d.String()
}
