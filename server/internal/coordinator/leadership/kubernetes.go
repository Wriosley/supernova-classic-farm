package leadership

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// KubernetesConfig configures Lease-based election.
type KubernetesConfig struct {
	Client        kubernetes.Interface
	Namespace     string
	LeaseName     string
	Identity      string
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
}

func (cfg KubernetesConfig) validate() error {
	if cfg.Client == nil {
		return errors.New("kubernetes client is required")
	}
	if strings.TrimSpace(cfg.Namespace) == "" || strings.TrimSpace(cfg.LeaseName) == "" {
		return errors.New("lease namespace and name are required")
	}
	if strings.TrimSpace(cfg.Identity) == "" {
		return errors.New("election identity is required")
	}
	if cfg.LeaseDuration <= 0 || cfg.RenewDeadline <= 0 || cfg.RetryPeriod <= 0 {
		return errors.New("election durations must be positive")
	}
	if !(cfg.LeaseDuration > cfg.RenewDeadline && cfg.RenewDeadline > cfg.RetryPeriod) {
		return errors.New("require leaseDuration > renewDeadline > retryPeriod")
	}
	return nil
}

// KubernetesElector elects leadership with a coordination Lease.
type KubernetesElector struct {
	cfg     KubernetesConfig
	tracker *Tracker
}

func NewKubernetesElector(cfg KubernetesConfig) (*KubernetesElector, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &KubernetesElector{cfg: cfg, tracker: NewTracker(cfg.Identity)}, nil
}

func (e *KubernetesElector) State() State { return e.tracker.State() }

func (e *KubernetesElector) Run(ctx context.Context, callbacks Callbacks) error {
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{Name: e.cfg.LeaseName, Namespace: e.cfg.Namespace},
		Client:    e.cfg.Client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: e.cfg.Identity,
		},
	}
	var activeGeneration uint64
	lec, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   e.cfg.LeaseDuration,
		RenewDeadline:   e.cfg.RenewDeadline,
		RetryPeriod:     e.cfg.RetryPeriod,
		ReleaseOnCancel: true,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leadCtx context.Context) {
				generation := e.tracker.BeginLeading()
				activeGeneration = generation
				if callbacks.OnStartedLeading != nil {
					callbacks.OnStartedLeading(leadCtx, generation)
				}
			},
			OnStoppedLeading: func() {
				generation := activeGeneration
				if e.tracker.EndLeading(generation) && callbacks.OnStoppedLeading != nil {
					callbacks.OnStoppedLeading(generation)
				}
			},
			OnNewLeader: func(identity string) {
				e.tracker.SetLeaderIdentity(identity)
				if callbacks.OnNewLeader != nil {
					callbacks.OnNewLeader(identity)
				}
			},
		},
		Name: e.cfg.LeaseName,
	})
	if err != nil {
		return fmt.Errorf("create leader elector: %w", err)
	}
	lec.Run(ctx)
	return ctx.Err()
}

// ElectionIdentity builds a stable-enough process identity for the Lease.
func ElectionIdentity(podName string) (string, error) {
	podName = strings.TrimSpace(podName)
	if podName == "" {
		podName = strings.TrimSpace(os.Getenv("HOSTNAME"))
	}
	if podName == "" {
		return "", errors.New("POD_NAME or HOSTNAME is required for election identity")
	}
	suffix, err := randomSuffix()
	if err != nil {
		return "", err
	}
	return podName + "_" + suffix, nil
}

func randomSuffix() (string, error) {
	buf := make([]byte, 8)
	if _, err := randomRead(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}
