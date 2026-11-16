package mirage

import (
	"math/rand/v2"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	infrav1 "github.com/luthermonson/cluster-api-provider-mirage/api/infrastructure/v1alpha1"
)

// Phase identifies which fake operation a duration is being computed for.
type Phase string

const (
	PhaseClusterProvision   Phase = "cluster-provision"
	PhaseMachineProvision   Phase = "machine-provision"
	PhaseBootstrapProvision Phase = "bootstrap-provision"
	PhaseNodeRegister       Phase = "node-register"
	PhaseDelete             Phase = "delete"
)

// Profile holds the configurable base durations and jitter for every fake
// operation. Durations are "base ± jitter"; the actual value is drawn uniformly
// from [base-jitter, base+jitter] and never goes below one second.
//
// Two presets are provided (see FastProfile / DemoProfile). Every value is also
// overridable per operation via flags/env, and per object via annotations
// (mirage.cluster.x-k8s.io/provision-duration, -jitter, -delete-duration).
type Profile struct {
	ClusterProvision time.Duration
	ClusterJitter    time.Duration

	MachineProvision time.Duration // fake "VM create"
	MachineJitter    time.Duration

	BootstrapProvision time.Duration
	BootstrapJitter    time.Duration

	// NodeRegister is an *additional* hold, after the fake VM is provisioned,
	// before the fake Node is registered (models kubelet boot + join).
	NodeRegister time.Duration
	NodeJitter   time.Duration

	Delete       time.Duration // fake deprovision ("VM destroy")
	DeleteJitter time.Duration

	// PollInterval is the maximum RequeueAfter used while an object is held in
	// an in-progress phase. ReconcileJitter adds a little noise to every
	// requeue so events do not all land on the same instant.
	PollInterval    time.Duration
	ReconcileJitter time.Duration
}

// FastProfile is tuned for CI / e2e: operations settle in a few seconds but
// still pass through their in-progress phases over real wall-clock time.
func FastProfile() Profile {
	return Profile{
		ClusterProvision:   3 * time.Second,
		ClusterJitter:      2 * time.Second,
		MachineProvision:   5 * time.Second,
		MachineJitter:      3 * time.Second,
		BootstrapProvision: 2 * time.Second,
		BootstrapJitter:    1 * time.Second,
		NodeRegister:       3 * time.Second,
		NodeJitter:         2 * time.Second,
		Delete:             3 * time.Second,
		DeleteJitter:       2 * time.Second,
		PollInterval:       2 * time.Second,
		ReconcileJitter:    500 * time.Millisecond,
	}
}

// DemoProfile is tuned for live demos: operations take tens of seconds, like a
// real cloud, so the lifecycle is pleasant to watch.
func DemoProfile() Profile {
	return Profile{
		ClusterProvision:   30 * time.Second,
		ClusterJitter:      10 * time.Second,
		MachineProvision:   40 * time.Second,
		MachineJitter:      20 * time.Second,
		BootstrapProvision: 15 * time.Second,
		BootstrapJitter:    5 * time.Second,
		NodeRegister:       20 * time.Second,
		NodeJitter:         10 * time.Second,
		Delete:             25 * time.Second,
		DeleteJitter:       10 * time.Second,
		PollInterval:       5 * time.Second,
		ReconcileJitter:    2 * time.Second,
	}
}

// base/jitter for a phase, before annotation overrides.
func (p Profile) phase(ph Phase) (base, jitter time.Duration) {
	switch ph {
	case PhaseClusterProvision:
		return p.ClusterProvision, p.ClusterJitter
	case PhaseMachineProvision:
		return p.MachineProvision, p.MachineJitter
	case PhaseBootstrapProvision:
		return p.BootstrapProvision, p.BootstrapJitter
	case PhaseNodeRegister:
		return p.NodeRegister, p.NodeJitter
	case PhaseDelete:
		return p.Delete, p.DeleteJitter
	default:
		return p.MachineProvision, p.MachineJitter
	}
}

// TargetSeconds picks a randomized target duration (in whole seconds, minimum 1)
// for the given phase, honoring per-object annotation overrides.
func (p Profile) TargetSeconds(obj metav1.Object, ph Phase) int64 {
	base, jitter := p.phase(ph)

	ann := obj.GetAnnotations()
	if ph == PhaseDelete {
		if v, ok := ann[infrav1.DeleteDurationAnnotation]; ok {
			if d, err := time.ParseDuration(v); err == nil {
				base = d
			}
		}
	} else {
		if v, ok := ann[infrav1.ProvisionDurationAnnotation]; ok {
			if d, err := time.ParseDuration(v); err == nil {
				base = d
			}
		}
		if v, ok := ann[infrav1.ProvisionJitterAnnotation]; ok {
			if d, err := time.ParseDuration(v); err == nil {
				jitter = d
			}
		}
	}

	secs := jittered(base, jitter).Seconds()
	n := int64(secs)
	if n < 1 {
		n = 1
	}
	return n
}

// jittered draws uniformly from [base-jitter, base+jitter].
func jittered(base, jitter time.Duration) time.Duration {
	if jitter <= 0 {
		return base
	}
	delta := time.Duration(rand.Int64N(int64(2*jitter))) - jitter
	d := base + delta
	if d < time.Second {
		d = time.Second
	}
	return d
}

// Elapsed reports whether a fake timer started at startedAt and lasting
// targetSeconds has completed, along with a RequeueAfter to use otherwise.
// The requeue is capped at PollInterval and carries a little jitter so that
// concurrent objects do not all re-reconcile on the same instant.
func (p Profile) Elapsed(startedAt *metav1.Time, targetSeconds int64) (done bool, requeueAfter time.Duration) {
	if startedAt == nil {
		return false, p.requeue(p.PollInterval)
	}
	remaining := time.Until(startedAt.Time.Add(time.Duration(targetSeconds) * time.Second))
	if remaining <= 0 {
		return true, 0
	}
	wait := remaining
	if wait > p.PollInterval {
		wait = p.PollInterval
	}
	return false, p.requeue(wait)
}

func (p Profile) requeue(d time.Duration) time.Duration {
	if d < time.Second {
		d = time.Second
	}
	if p.ReconcileJitter > 0 {
		d += time.Duration(rand.Int64N(int64(p.ReconcileJitter)))
	}
	return d
}
