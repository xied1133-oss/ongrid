package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	edgek8s "github.com/ongridio/ongrid/internal/edgeagent/k8s"
)

func runK8sUpgradeCommand(ctx context.Context, args []string) (bool, error) {
	if len(args) == 0 || args[0] != "prepare-k8s-upgrade" {
		return false, nil
	}
	flags := flag.NewFlagSet("prepare-k8s-upgrade", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	namespace := flags.String("namespace", "", "release namespace")
	controller := flags.String("controller", "", "controller Deployment name")
	metricsScraper := flags.String("metrics-scraper", "", "metrics scraper Deployment name")
	gatewayMode := flags.String("gateway-mode", "", "target telemetry gateway mode")
	metricsMode := flags.String("metrics-mode", "", "target Kubernetes metrics mode")
	timeout := flags.Duration("timeout", 8*time.Minute, "preparation timeout")
	if err := flags.Parse(args[1:]); err != nil {
		return true, fmt.Errorf("parse prepare-k8s-upgrade arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return true, fmt.Errorf("prepare-k8s-upgrade: unexpected arguments %q", strings.Join(flags.Args(), " "))
	}
	if *timeout <= 0 {
		return true, fmt.Errorf("prepare-k8s-upgrade: timeout must be positive")
	}
	prepareCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	return true, edgek8s.PrepareUpgrade(prepareCtx, edgek8s.UpgradePreparationConfig{
		Namespace:                *namespace,
		ControllerDeployment:     *controller,
		MetricsScraperDeployment: *metricsScraper,
		TargetGatewayMode:        *gatewayMode,
		TargetMetricsMode:        *metricsMode,
	})
}
