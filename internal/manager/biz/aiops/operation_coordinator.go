package aiops

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// OperationReconciler advances one durable Operation kind. Domain packages
// own their remote polling and artifact semantics; this coordinator owns the
// common lifecycle of periodic, restart-safe reconciliation.
type OperationReconciler interface {
	Kind() string
	Reconcile(ctx context.Context) error
}

type OperationCoordinator struct {
	interval time.Duration
	log      *slog.Logger
	mu       sync.RWMutex
	items    map[string]OperationReconciler
}

func NewOperationCoordinator(interval time.Duration, log *slog.Logger) *OperationCoordinator {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &OperationCoordinator{interval: interval, log: log, items: make(map[string]OperationReconciler)}
}

func (c *OperationCoordinator) Register(reconciler OperationReconciler) error {
	if c == nil || reconciler == nil || reconciler.Kind() == "" {
		return fmt.Errorf("operation coordinator: reconciler kind is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[reconciler.Kind()]; exists {
		return fmt.Errorf("operation coordinator: duplicate reconciler %q", reconciler.Kind())
	}
	c.items[reconciler.Kind()] = reconciler
	return nil
}

func (c *OperationCoordinator) Run(ctx context.Context) error {
	if c == nil {
		return nil
	}
	run := func() {
		c.mu.RLock()
		items := make([]OperationReconciler, 0, len(c.items))
		for _, item := range c.items {
			items = append(items, item)
		}
		c.mu.RUnlock()
		for _, item := range items {
			if err := item.Reconcile(ctx); err != nil && ctx.Err() == nil {
				c.log.Warn("operation reconcile failed", slog.String("kind", item.Kind()), slog.Any("err", err))
			}
		}
	}
	run()
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			run()
		}
	}
}
