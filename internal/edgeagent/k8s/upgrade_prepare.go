package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	telemetryBackendLabel                = "ongrid.io/telemetry-backend"
	kubernetesStrategicMergePatchContent = "application/strategic-merge-patch+json"
	defaultUpgradePollInterval           = time.Second
)

// UpgradePreparationConfig describes the live release transition performed by
// the Helm pre-upgrade hook. The hook mutates only the two data-plane owner
// Deployments and is safe to run repeatedly.
type UpgradePreparationConfig struct {
	Namespace                string
	ControllerDeployment     string
	MetricsScraperDeployment string
	TargetGatewayMode        string
	TargetMetricsMode        string
	PollInterval             time.Duration
}

// PrepareUpgrade makes a single Helm release safe even when Kubernetes
// reconciles the final Controller and data-plane Deployments in either order.
// It runs before Helm applies the new manifests.
func PrepareUpgrade(ctx context.Context, cfg UpgradePreparationConfig) error {
	client, err := newInClusterAPIClient()
	if err != nil {
		return fmt.Errorf("prepare kubernetes upgrade client: %w", err)
	}
	return prepareUpgradeWithClient(ctx, client, cfg)
}

func prepareUpgradeWithClient(ctx context.Context, client *apiClient, cfg UpgradePreparationConfig) error {
	if client == nil {
		return errors.New("prepare kubernetes upgrade: api client is required")
	}
	cfg.Namespace = strings.TrimSpace(cfg.Namespace)
	if cfg.Namespace == "" {
		cfg.Namespace = strings.TrimSpace(client.namespace)
	}
	if cfg.Namespace == "" {
		return errors.New("prepare kubernetes upgrade: namespace is required")
	}
	cfg.ControllerDeployment = strings.TrimSpace(cfg.ControllerDeployment)
	if cfg.ControllerDeployment == "" {
		return errors.New("prepare kubernetes upgrade: controller deployment is required")
	}
	cfg.MetricsScraperDeployment = strings.TrimSpace(cfg.MetricsScraperDeployment)
	if cfg.MetricsScraperDeployment == "" {
		return errors.New("prepare kubernetes upgrade: metrics scraper deployment is required")
	}
	cfg.TargetGatewayMode = strings.TrimSpace(cfg.TargetGatewayMode)
	if cfg.TargetGatewayMode != "embedded" && cfg.TargetGatewayMode != "deployment" {
		return fmt.Errorf("prepare kubernetes upgrade: unsupported gateway mode %q", cfg.TargetGatewayMode)
	}
	cfg.TargetMetricsMode = strings.TrimSpace(cfg.TargetMetricsMode)
	if cfg.TargetMetricsMode != "controller" && cfg.TargetMetricsMode != "scraper" {
		return fmt.Errorf("prepare kubernetes upgrade: unsupported metrics mode %q", cfg.TargetMetricsMode)
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultUpgradePollInterval
	}

	if cfg.TargetMetricsMode == "controller" {
		if err := stopDeploymentIfPresent(ctx, client, cfg.Namespace, cfg.MetricsScraperDeployment, cfg.PollInterval); err != nil {
			return fmt.Errorf("stop previous metrics scraper: %w", err)
		}
	}

	controller, err := getUpgradeDeployment(ctx, client, cfg.Namespace, cfg.ControllerDeployment)
	if err != nil {
		return fmt.Errorf("get controller deployment: %w", err)
	}
	container, ok := controllerContainer(controller)
	if !ok {
		return fmt.Errorf("controller deployment %s/%s has no edge-controller container", cfg.Namespace, cfg.ControllerDeployment)
	}

	labels := map[string]string{}
	envPatches := make([]map[string]any, 0, 2)
	changed := false
	if containerHasPort(container, "otlp-grpc") && controller.Spec.Template.Metadata.Labels[telemetryBackendLabel] != "true" {
		// The Service switches to this stable selector in the final release.
		// Mark the still-embedded Controller first so changing the selector does
		// not itself disconnect the old endpoint. This is needed for both target
		// modes when upgrading a Chart that still selected component=controller.
		labels[telemetryBackendLabel] = "true"
		changed = true
	}
	if cfg.TargetMetricsMode == "scraper" {
		if containerHasEnv(container, "ONGRID_K8S_METRICS_ENDPOINT") {
			envPatches = append(envPatches, map[string]any{
				"name":   "ONGRID_K8S_METRICS_ENDPOINT",
				"$patch": "delete",
			})
			changed = true
		}
		if value, found := containerEnvValue(container, "ONGRID_K8S_APP_METRICS_DISCOVERY"); found && value != "false" {
			envPatches = append(envPatches, map[string]any{
				"name":      "ONGRID_K8S_APP_METRICS_DISCOVERY",
				"value":     "false",
				"valueFrom": nil,
			})
			changed = true
		}
	}
	if !changed {
		return nil
	}

	templatePatch := map[string]any{}
	if len(labels) > 0 {
		templatePatch["metadata"] = map[string]any{"labels": labels}
	}
	if len(envPatches) > 0 {
		templatePatch["spec"] = map[string]any{
			"containers": []map[string]any{{
				"name": "edge-controller",
				"env":  envPatches,
			}},
		}
	}
	patch := map[string]any{"spec": map[string]any{"template": templatePatch}}
	updated, err := patchUpgradeDeployment(ctx, client, cfg.Namespace, cfg.ControllerDeployment, patch)
	if err != nil {
		return fmt.Errorf("prepare controller deployment: %w", err)
	}
	if err := waitUpgradeDeploymentReady(ctx, client, cfg.Namespace, cfg.ControllerDeployment, updated.Metadata.Generation, cfg.PollInterval); err != nil {
		return fmt.Errorf("wait for prepared controller: %w", err)
	}
	return nil
}

type upgradeDeployment struct {
	Metadata struct {
		Generation int64 `json:"generation"`
	} `json:"metadata"`
	Spec struct {
		Replicas *int `json:"replicas"`
		Template struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Containers []upgradeContainer `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		ObservedGeneration  int64 `json:"observedGeneration"`
		UpdatedReplicas     int   `json:"updatedReplicas"`
		ReadyReplicas       int   `json:"readyReplicas"`
		AvailableReplicas   int   `json:"availableReplicas"`
		UnavailableReplicas int   `json:"unavailableReplicas"`
	} `json:"status"`
}

type upgradeContainer struct {
	Name  string `json:"name"`
	Ports []struct {
		Name string `json:"name"`
	} `json:"ports"`
	Env []struct {
		Name      string          `json:"name"`
		Value     string          `json:"value"`
		ValueFrom json.RawMessage `json:"valueFrom"`
	} `json:"env"`
}

func controllerContainer(deployment *upgradeDeployment) (upgradeContainer, bool) {
	if deployment == nil {
		return upgradeContainer{}, false
	}
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == "edge-controller" {
			return container, true
		}
	}
	return upgradeContainer{}, false
}

func containerHasPort(container upgradeContainer, name string) bool {
	for _, port := range container.Ports {
		if port.Name == name {
			return true
		}
	}
	return false
}

func containerHasEnv(container upgradeContainer, name string) bool {
	_, found := containerEnvValue(container, name)
	return found
}

func containerEnvValue(container upgradeContainer, name string) (string, bool) {
	for _, env := range container.Env {
		if env.Name == name {
			return strings.TrimSpace(env.Value), true
		}
	}
	return "", false
}

func stopDeploymentIfPresent(ctx context.Context, client *apiClient, namespace, name string, pollInterval time.Duration) error {
	deployment, err := getUpgradeDeployment(ctx, client, namespace, name)
	if errors.Is(err, errNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if deploymentReplicas(deployment) == 0 {
		return nil
	}
	updated, err := patchUpgradeDeployment(ctx, client, namespace, name, map[string]any{
		"spec": map[string]any{"replicas": 0},
	})
	if err != nil {
		return err
	}
	return waitUpgradeDeploymentReady(ctx, client, namespace, name, updated.Metadata.Generation, pollInterval)
}

func getUpgradeDeployment(ctx context.Context, client *apiClient, namespace, name string) (*upgradeDeployment, error) {
	var deployment upgradeDeployment
	if err := client.get(ctx, deploymentAPIPath(namespace, name), &deployment); err != nil {
		return nil, err
	}
	return &deployment, nil
}

func patchUpgradeDeployment(ctx context.Context, client *apiClient, namespace, name string, patch map[string]any) (*upgradeDeployment, error) {
	body, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("marshal deployment patch: %w", err)
	}
	out, err := client.doRaw(ctx, http.MethodPatch, deploymentAPIPath(namespace, name), kubernetesStrategicMergePatchContent, body)
	if err != nil {
		return nil, err
	}
	var deployment upgradeDeployment
	if err := json.Unmarshal(out, &deployment); err != nil {
		return nil, fmt.Errorf("decode patched deployment: %w", err)
	}
	return &deployment, nil
}

func waitUpgradeDeploymentReady(ctx context.Context, client *apiClient, namespace, name string, generation int64, pollInterval time.Duration) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		deployment, err := getUpgradeDeployment(ctx, client, namespace, name)
		if err != nil {
			return err
		}
		if deploymentReady(deployment, generation) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("deployment %s/%s rollout: %w", namespace, name, ctx.Err())
		case <-ticker.C:
		}
	}
}

func deploymentReady(deployment *upgradeDeployment, generation int64) bool {
	if deployment == nil || deployment.Status.ObservedGeneration < generation {
		return false
	}
	desired := deploymentReplicas(deployment)
	return deployment.Status.UpdatedReplicas == desired &&
		deployment.Status.ReadyReplicas == desired &&
		deployment.Status.AvailableReplicas == desired &&
		deployment.Status.UnavailableReplicas == 0
}

func deploymentReplicas(deployment *upgradeDeployment) int {
	if deployment == nil || deployment.Spec.Replicas == nil {
		return 1
	}
	return *deployment.Spec.Replicas
}

func deploymentAPIPath(namespace, name string) string {
	return "/apis/apps/v1/namespaces/" + url.PathEscape(namespace) + "/deployments/" + url.PathEscape(name)
}
