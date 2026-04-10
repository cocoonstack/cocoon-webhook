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
	// reaperDefaultInterval is how often the reaper sweeps the
	// affinity ConfigMaps for orphan reservations.
	reaperDefaultInterval = 5 * time.Minute
	// reaperDefaultGrace is how long an orphan reservation is kept
	// before being released, to absorb brief windows where a pod is
	// recreated under the same name without the webhook's mutate
	// being involved.
	reaperDefaultGrace = 30 * time.Minute
)

// Reaper periodically scans every per-pool affinity ConfigMap and
// releases reservations whose backing pod no longer exists.
//
// Pod liveness checks are served from a shared pod informer cache,
// so a sweep across N reservations is N in-memory lookups instead
// of N apiserver GETs.
type Reaper struct {
	Store     Store
	Client    kubernetes.Interface
	PodLister corelisters.PodLister
	Interval  time.Duration
	Grace     time.Duration
}

// NewReaper constructs a Reaper with sensible defaults filled in for
// any zero-valued field. The pod lister must come from a started,
// cache-synced informer; the client is only used to list the
// per-pool affinity ConfigMaps.
func NewReaper(store Store, client kubernetes.Interface, pods corelisters.PodLister) *Reaper {
	return &Reaper{
		Store:     store,
		Client:    client,
		PodLister: pods,
		Interval:  reaperDefaultInterval,
		Grace:     reaperDefaultGrace,
	}
}

// Run drives the reap loop until ctx is canceled. The first sweep
// happens immediately so a webhook restart doesn't have to wait
// Interval before reclaiming stale slots.
func (r *Reaper) Run(ctx context.Context) {
	logger := log.WithFunc("Reaper.Run")

	if err := r.reapOnce(ctx); err != nil {
		logger.Warnf(ctx, "initial sweep: %v", err)
	}

	ticker := time.NewTicker(r.Interval)
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

// reapOnce performs a single sweep across every cocoon-affinity
// ConfigMap and releases stale entries.
func (r *Reaper) reapOnce(ctx context.Context) error {
	logger := log.WithFunc("Reaper.reapOnce")
	pools, err := r.discoverPools(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, pool := range pools {
		entries, err := r.Store.List(ctx, pool)
		if err != nil {
			logger.Warnf(ctx, "list reservations for pool %s: %v", pool, err)
			continue
		}
		for _, entry := range entries {
			if !r.shouldRelease(ctx, entry, now) {
				continue
			}
			if err := r.Store.Release(ctx, entry.Pool, entry.Namespace, entry.Deployment, entry.Slot); err != nil {
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

// discoverPools lists every per-pool ConfigMap in the cocoon system
// namespace by label and returns the pool names.
func (r *Reaper) discoverPools(ctx context.Context) ([]string, error) {
	cms, err := r.Client.CoreV1().ConfigMaps(systemNamespace).List(ctx, metav1.ListOptions{
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

// shouldRelease decides whether a reservation is stale enough to drop.
// Conditions: the pod no longer exists in its namespace, AND the
// reservation is older than the grace window.
func (r *Reaper) shouldRelease(_ context.Context, entry Reservation, now time.Time) bool {
	if entry.Pod == "" {
		return false
	}
	if now.Sub(entry.UpdatedAt) < r.Grace {
		return false
	}
	_, err := r.PodLister.Pods(entry.Namespace).Get(entry.Pod)
	if err == nil {
		return false
	}
	return apierrors.IsNotFound(err)
}
