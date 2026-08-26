package edge

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/ongridio/ongrid/internal/pkg/errs"
)

func validateCustomMetricsSpec(spec map[string]interface{}) error {
	if spec == nil {
		return nil
	}
	rawTargets, ok := spec["targets"]
	if !ok {
		return nil
	}
	targets, ok := rawTargets.([]interface{})
	if !ok {
		return fmt.Errorf("%w: custommetrics.targets must be an array", errs.ErrInvalid)
	}
	seenIDs := map[string]struct{}{}
	seenURLs := map[string]string{}
	for i, raw := range targets {
		target, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%w: custommetrics.targets[%d] must be an object", errs.ErrInvalid, i)
		}
		id := mapString(target, "id")
		if id == "" {
			return fmt.Errorf("%w: custommetrics.targets[%d].id required", errs.ErrInvalid, i)
		}
		if _, exists := seenIDs[id]; exists {
			return fmt.Errorf("%w: custommetrics.targets[%d] duplicate id %q", errs.ErrInvalid, i, id)
		}
		seenIDs[id] = struct{}{}
		targetURL := mapString(target, "target_url")
		if targetURL == "" {
			return fmt.Errorf("%w: custommetrics.targets[%d].target_url required", errs.ErrInvalid, i)
		}
		urlKey, err := canonicalCustomMetricsTargetURL(targetURL)
		if err != nil {
			return fmt.Errorf("%w: custommetrics.targets[%d].target_url: %v", errs.ErrInvalid, i, err)
		}
		if prevID, exists := seenURLs[urlKey]; exists {
			return fmt.Errorf("%w: custommetrics.targets[%d] duplicate target_url %q conflicts with target %q", errs.ErrInvalid, i, targetURL, prevID)
		}
		seenURLs[urlKey] = id
		if err := validateCustomMetricsTargetResource(i, target); err != nil {
			return err
		}
	}
	return nil
}

func validateCustomMetricsTargetResource(i int, target map[string]interface{}) error {
	if mapString(target, "resource_type") != "" || mapString(target, "db_type") != "" {
		return fmt.Errorf("%w: custommetrics.targets[%d] use resource.category/type instead of top-level resource_type/db_type", errs.ErrInvalid, i)
	}
	rawResource, exists := target["resource"]
	if !exists || rawResource == nil {
		return nil
	}
	resource, ok := mapValue(rawResource)
	if !ok {
		return fmt.Errorf("%w: custommetrics.targets[%d].resource must be an object", errs.ErrInvalid, i)
	}
	category := normalizeCustomMetricsResourceCategory(mapString(resource, "category"))
	if category == "" {
		return fmt.Errorf("%w: custommetrics.targets[%d].resource.category required", errs.ErrInvalid, i)
	}
	if !customMetricsResourceCategorySupported(category) {
		return fmt.Errorf("%w: custommetrics.targets[%d].resource.category unsupported %q", errs.ErrInvalid, i, mapString(resource, "category"))
	}
	if category != "database" {
		return nil
	}
	rawDBType := mapString(resource, "type")
	if rawDBType == "" {
		return fmt.Errorf("%w: custommetrics.targets[%d].resource.type required when category is database", errs.ErrInvalid, i)
	}
	dbType := normalizeCustomMetricsDBType(rawDBType)
	if !customMetricsDBTypeSupported(dbType) {
		return fmt.Errorf("%w: custommetrics.targets[%d].resource.type unsupported %q", errs.ErrInvalid, i, rawDBType)
	}
	return nil
}

func normalizeCustomMetricsResourceCategory(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return ""
	case "database":
		return "database"
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}

func customMetricsResourceCategorySupported(v string) bool {
	switch v {
	case "database":
		return true
	default:
		return false
	}
}

func normalizeCustomMetricsDBType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "postgres", "pg":
		return "postgresql"
	case "mongo":
		return "mongodb"
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}

func customMetricsDBTypeSupported(v string) bool {
	switch v {
	case "mysql", "postgresql", "redis", "mongodb":
		return true
	default:
		return false
	}
}

func canonicalCustomMetricsTargetURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("missing host")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}
