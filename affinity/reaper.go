package affinity

import (
	"context"
	"fmt"
	"time"

	"github.com/projecteru2/core/log"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"

	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

const (
	DefaultReaperInterval = 5 * time.Minute
	// DefaultReaperGrace absorbs brief windows where a pod is recreated under the same name.
	DefaultReaperGrace = 30 * time.Minute
)

// Reaper releases orphan reservations whose backing pod no longer exists.
type Reaper struct {
	store     Store
	client    kubernetes.Interface
	podLister corelisters.PodLister
	interval  time.Duration
	grace     time.Duration
}

func NewReaper(store Store, client kubernetes.Interface, pods corelisters.PodLister, interval, grace time.Duration) *Reaper {
	if interval <= 0 {
		interval = DefaultReaperInterval
	}
	if grace <= 0 {
		grace = DefaultReaperGrace
	}
	return &Reaper{
		store:     store,
		client:    client,
		podLister: pods,
		interval:  interval,
		grace:     grace,
	}
}

// Run sweeps immediately, then on each tick until ctx is canceled.
func (r *Reaper) Run(ctx context.Context) {
	logger := log.WithFunc("Reaper.Run")

	if err := r.reapOnce(ctx); err != nil {
		logger.Warnf(ctx, "initial sweep: %v", err)
	}

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.reapOnce(ctx); err != nil {
				logger.Warnf(ctx, "sweep: %v", err)
			}
		}
	}
}

func (r *Reaper) reapOnce(ctx context.Context) error {
	logger := log.WithFunc("Reaper.reapOnce")
	pools, err := r.discoverPools(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, pool := range pools {
		entries, err := r.store.List(ctx, pool)
		if err != nil {
			logger.Warnf(ctx, "list reservations for pool %s: %v", pool, err)
			continue
		}
		for _, entry := range entries {
			if !r.shouldRelease(entry, now) {
				continue
			}
			if err := r.store.Release(ctx, entry.Pool, entry.Namespace, entry.Deployment, entry.Slot); err != nil {
				logger.Warnf(ctx, "release %s/%s slot %d: %v", entry.Namespace, entry.Deployment, entry.Slot, err)
				continue
			}
			metrics.RecordRelease(entry.Pool)
			logger.Infof(ctx, "released orphan reservation pool=%s ns=%s deploy=%s slot=%d pod=%s",
				entry.Pool, entry.Namespace, entry.Deployment, entry.Slot, entry.Pod)
		}
	}
	return nil
}

func (r *Reaper) discoverPools(ctx context.Context) ([]string, error) {
	cms, err := r.client.CoreV1().ConfigMaps(systemNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: meta.LabelManagedBy + "=" + managedByValue,
	})
	if err != nil {
		return nil, fmt.Errorf("list affinity configmaps: %w", err)
	}
	pools := make([]string, 0, len(cms.Items))
	for _, cm := range cms.Items {
		if pool, ok := cm.Labels[meta.LabelNodePool]; ok && pool != "" {
			pools = append(pools, pool)
		}
	}
	return pools, nil
}

// shouldRelease: pod must be gone AND reservation must be older than grace.
func (r *Reaper) shouldRelease(entry Reservation, now time.Time) bool {
	if entry.Pod == "" {
		return false
	}
	if now.Sub(entry.UpdatedAt) < r.grace {
		return false
	}
	_, err := r.podLister.Pods(entry.Namespace).Get(entry.Pod)
	if err == nil {
		return false
	}
	return apierrors.IsNotFound(err)
}
