package leadership

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestKubernetesConfigValidation(t *testing.T) {
	client := fake.NewSimpleClientset()
	_, err := NewKubernetesElector(KubernetesConfig{
		Client: client, Namespace: "ns", LeaseName: "lease", Identity: "a",
		LeaseDuration: 15 * time.Second, RenewDeadline: 10 * time.Second, RetryPeriod: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewKubernetesElector(KubernetesConfig{
		Client: client, Namespace: "ns", LeaseName: "lease", Identity: "a",
		LeaseDuration: 5 * time.Second, RenewDeadline: 10 * time.Second, RetryPeriod: 2 * time.Second,
	}); err == nil {
		t.Fatal("accepted invalid duration ordering")
	}
}

func TestKubernetesElectorSingleLeader(t *testing.T) {
	client := fake.NewSimpleClientset()
	cfg := KubernetesConfig{
		Client: client, Namespace: "classic-farm", LeaseName: "classic-farm-coordinator",
		Identity: "coord-a", LeaseDuration: 2 * time.Second, RenewDeadline: 1 * time.Second, RetryPeriod: 200 * time.Millisecond,
	}
	elector, err := NewKubernetesElector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	leading := make(chan uint64, 1)
	go func() {
		_ = elector.Run(ctx, Callbacks{
			OnStartedLeading: func(_ context.Context, generation uint64) {
				leading <- generation
			},
		})
	}()
	select {
	case gen := <-leading:
		if gen == 0 || !elector.State().IsLeader {
			t.Fatalf("bad lead state gen=%d state=%+v", gen, elector.State())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not acquire lease")
	}
	lease, err := client.CoordinationV1().Leases("classic-farm").Get(context.Background(), "classic-farm-coordinator", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != "coord-a" {
		t.Fatalf("unexpected holder: %+v", lease.Spec.HolderIdentity)
	}
	cancel()
}
