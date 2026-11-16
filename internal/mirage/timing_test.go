package mirage

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	infrav1 "github.com/luthermonson/cluster-api-provider-mirage/api/infrastructure/v1alpha1"
)

func obj(ann map[string]string) metav1.Object {
	return &metav1.ObjectMeta{Annotations: ann}
}

func TestTargetSecondsWithinJitterBounds(t *testing.T) {
	p := Profile{MachineProvision: 20 * time.Second, MachineJitter: 5 * time.Second}
	for i := 0; i < 200; i++ {
		s := p.TargetSeconds(obj(nil), PhaseMachineProvision)
		if s < 15 || s > 25 {
			t.Fatalf("target %ds outside [15,25]", s)
		}
	}
}

func TestTargetSecondsNeverBelowOne(t *testing.T) {
	p := Profile{MachineProvision: 0, MachineJitter: 0}
	if s := p.TargetSeconds(obj(nil), PhaseMachineProvision); s < 1 {
		t.Fatalf("target %ds must be >= 1", s)
	}
}

func TestAnnotationOverride(t *testing.T) {
	p := Profile{MachineProvision: 20 * time.Second, MachineJitter: 5 * time.Second}
	o := obj(map[string]string{
		infrav1.ProvisionDurationAnnotation: "90s",
		infrav1.ProvisionJitterAnnotation:   "0s",
	})
	if s := p.TargetSeconds(o, PhaseMachineProvision); s != 90 {
		t.Fatalf("annotation override: got %ds want 90s", s)
	}
}

func TestDeleteAnnotationOverride(t *testing.T) {
	p := Profile{Delete: 10 * time.Second, DeleteJitter: 0}
	o := obj(map[string]string{infrav1.DeleteDurationAnnotation: "45s"})
	if s := p.TargetSeconds(o, PhaseDelete); s != 45 {
		t.Fatalf("delete override: got %ds want 45s", s)
	}
}

func TestElapsed(t *testing.T) {
	p := Profile{PollInterval: 2 * time.Second}

	// nil start -> not done, requeue ~ poll interval
	if done, after := p.Elapsed(nil, 10); done || after <= 0 {
		t.Fatalf("nil start: done=%v after=%v", done, after)
	}

	// started long ago -> done
	past := metav1.NewTime(time.Now().Add(-time.Hour))
	if done, _ := p.Elapsed(&past, 10); !done {
		t.Fatalf("past timer should be done")
	}

	// just started, long target -> not done, requeue capped at PollInterval
	now := metav1.Now()
	done, after := p.Elapsed(&now, 3600)
	if done {
		t.Fatalf("fresh long timer should not be done")
	}
	if after > p.PollInterval+p.ReconcileJitter {
		t.Fatalf("requeue %v exceeds poll interval cap", after)
	}
}
