package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	devicebiz "github.com/ongridio/ongrid/internal/manager/biz/device"
	topologybiz "github.com/ongridio/ongrid/internal/manager/biz/topology"
	alertmodel "github.com/ongridio/ongrid/internal/manager/model/alert"
	topologymodel "github.com/ongridio/ongrid/internal/manager/model/topology"
)

// topology_impact.go — deterministic blast-radius computation shared by
// expand_topology (the LLM-driven tool) and correlate_incident's
// topology_impact panel.
//
// Why correlate_incident carries topology at all: production RCA reports were
// silently dropping the 拓扑影响面 section. Root cause was NOT that the
// topology tools were unavailable — turning off toolbag deferral (full schemas
// visible) still left small models never calling expand_topology on their own
// under the tool-call budget. The fix is to stop depending on model initiative:
// correlate_incident is the one tool every investigation calls first, so it now
// computes the blast radius server-side and hands it over. The model only has
// to write up what it's given.

const (
	// topologyImpactMaxHops bounds the deterministic panel. 2 hops surfaces
	// "the failing node + its direct dependents + one cascade level" without
	// spamming the bundle; matches expand_topology's own default depth.
	topologyImpactMaxHops = 2

	// topologyImpactMaxAffected caps how many neighbours land in the bundle so
	// a hub node (e.g. a shared gateway) can't blow correlate_incident's
	// response-size cap. Nearest-first (hits are pre-sorted by hops).
	topologyImpactMaxAffected = 15
)

// topologyServiceLabelKeys are incident-label keys that commonly carry a
// service / container / workload name, in priority order. metric_raw rules
// merge the firing series' labels onto the incident (alert/evaluators_phaseA
// mergeLabels(..., ent.Metric, ...)), so a rule over qms_container_up{name=
// "deepway-ppap"} lands the name here. Resolving the SERVICE node — not just
// the device node — is what lets the blast radius express "who depends on
// deepway-ppap" instead of merely "what else runs on this host".
var topologyServiceLabelKeys = []string{"service", "name", "container", "app", "workload", "pod", "deployment"}

// topologyDeviceLabelKeys are incident-label keys that carry a device registry
// ID (devices.id). device_offline / host-scoped rules land the id here, but the
// alert_incidents.device_id COLUMN is frequently NULL for them (the pipeline
// merges the firing series' labels rather than populating the column), so the
// device-node fallback below must also read these labels — otherwise a host-down
// incident, exactly where blast radius matters most (every service deployed_on
// the host), resolves to no topology node at all.
var topologyDeviceLabelKeys = []string{"device_id", "device", "host_id"}

// expandBlastRadius walks the business-topology graph outward from startID,
// following propagating relations (depends_on / deployed_on / routes_to) when
// onlyPropagating is set, up to depth hops. It returns the center node and
// every reachable node with the hop count, the relation type that led there,
// and that relation's AIOps semantics tag.
//
// This is the single source of truth for propagation semantics: expand_topology
// exposes it verbatim to the LLM, and correlate_incident's topology_impact
// panel consumes it deterministically, so a report's 拓扑影响面 section can
// never contradict what expand_topology would return for the same node.
func expandBlastRadius(ctx context.Context, topo *topologybiz.Usecase, startID uint64, depth int, onlyPropagating bool, direction string) (*topologymodel.Node, []expandTopologyHit, error) {
	center, err := topo.GetNode(ctx, startID)
	if err != nil {
		return nil, nil, fmt.Errorf("get center node %d: %w", startID, err)
	}

	// Pull every registered relation type once into a name → metadata map; we
	// need direction + propagates_failure per hop and loading a handful of rows
	// beats re-fetching per edge.
	rts, err := topo.ListRelationTypes(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list relation types: %w", err)
	}
	typeMeta := make(map[string]*topologymodel.RelationType, len(rts))
	for _, rt := range rts {
		typeMeta[rt.Name] = rt
	}

	// Pull all relations once and keep the BFS in-memory. For tenant-scale
	// (≤10k relations per working assumption) this beats N+1 per-node lookups.
	allRel, _, err := topo.ListRelations(ctx, topologybiz.RelationListFilter{Limit: 10000})
	if err != nil {
		return nil, nil, fmt.Errorf("list relations: %w", err)
	}

	// BFS. Track (node_id → first-reach metadata) so we don't re-visit a node
	// with a worse (longer) path.
	type visit struct {
		hops         int
		relationType string
		semanticsTag string
		propagates   bool
		via          uint64
		reachedVia   string // downstream | upstream
	}
	visited := map[uint64]visit{startID: {hops: 0}}
	queue := []uint64{startID}
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		hops := visited[cur].hops
		if hops >= depth {
			continue
		}
		for _, r := range allRel {
			rt := typeMeta[r.Type]
			if rt == nil {
				continue
			}
			if onlyPropagating && !rt.PropagatesFailure {
				continue
			}
			var nextID uint64
			var reach string
			switch {
			case r.SrcID == cur && (direction == "both" || direction == "downstream"):
				nextID = r.DstID
				reach = "downstream"
			case r.DstID == cur && (direction == "both" || direction == "upstream"):
				nextID = r.SrcID
				reach = "upstream"
			default:
				continue
			}
			if _, seen := visited[nextID]; seen {
				continue
			}
			visited[nextID] = visit{
				hops:         hops + 1,
				relationType: r.Type,
				semanticsTag: rt.SemanticsTag,
				propagates:   rt.PropagatesFailure,
				via:          cur,
				reachedVia:   reach,
			}
			queue = append(queue, nextID)
		}
	}

	// Hydrate node detail for everyone visited (skip the center, we already
	// have it). Missing nodes (stale relations) are dropped, not fatal.
	otherIDs := make([]uint64, 0, len(visited))
	for id := range visited {
		if id == startID {
			continue
		}
		otherIDs = append(otherIDs, id)
	}
	nodes := fetchTopologyNodesByIDs(ctx, topo, otherIDs)

	hits := make([]expandTopologyHit, 0, len(otherIDs))
	for id, v := range visited {
		if id == startID {
			continue
		}
		n := nodes[id]
		if n == nil {
			continue // shouldn't happen — relation pointed at nothing
		}
		viaName := ""
		if v.via != 0 {
			if vn := nodes[v.via]; vn != nil {
				viaName = vn.Name
			} else if v.via == startID {
				viaName = center.Name
			}
		}
		hits = append(hits, expandTopologyHit{
			NodeID:       n.ID,
			NodeName:     n.Name,
			NodeType:     n.Type,
			Hops:         v.hops,
			RelationType: v.relationType,
			SemanticsTag: v.semanticsTag,
			Propagates:   v.propagates,
			ReachedVia:   v.reachedVia,
			ViaNodeID:    v.via,
			ViaNodeName:  viaName,
		})
	}
	// Stable sort: nearer first, then by name.
	sortHitsByHopsThenName(hits)
	return center, hits, nil
}

// fetchTopologyNodesByIDs hydrates nodes best-effort. The topology Usecase
// doesn't expose GetMany today; a stale relation pointing at a deleted node is
// skipped rather than failing the whole walk.
func fetchTopologyNodesByIDs(ctx context.Context, topo *topologybiz.Usecase, ids []uint64) map[uint64]*topologymodel.Node {
	out := make(map[uint64]*topologymodel.Node, len(ids))
	for _, id := range ids {
		n, err := topo.GetNode(ctx, id)
		if err != nil {
			continue
		}
		out[id] = n
	}
	return out
}

// topologyImpactPanel resolves the incident's own topology node and returns its
// propagating blast radius, computed server-side. This is the deterministic
// backbone of the report's 拓扑影响面 section.
//
// Best-effort by contract: returns (nil, reason) whenever the graph or the
// incident's node can't be resolved, so correlate_incident degrades to
// Skipped["topology_impact"] (visible to the LLM) instead of failing the whole
// bundle.
func topologyImpactPanel(ctx context.Context, topo *topologybiz.Usecase, devices *devicebiz.Usecase, inc *alertmodel.Incident, labels, annotations map[string]string) (*topologyImpact, string) {
	if topo == nil {
		return nil, "topology graph not configured"
	}
	startID, reason := resolveIncidentTopologyNode(ctx, topo, devices, inc, labels, annotations)
	if startID == 0 {
		return nil, reason
	}
	center, hits, err := expandBlastRadius(ctx, topo, startID, topologyImpactMaxHops, true, "both")
	if err != nil {
		return nil, "topology walk failed: " + err.Error()
	}
	panel := &topologyImpact{
		Center:  topologyNodeRef{NodeID: center.ID, Name: center.Name, Type: center.Type},
		MaxHops: topologyImpactMaxHops,
	}
	for _, h := range hits {
		if len(panel.Affected) >= topologyImpactMaxAffected {
			panel.Note = fmt.Sprintf("blast radius truncated to %d nearest nodes", topologyImpactMaxAffected)
			break
		}
		panel.Affected = append(panel.Affected, topologyAffected{
			NodeID:       h.NodeID,
			Name:         h.NodeName,
			Type:         h.NodeType,
			Hops:         h.Hops,
			RelationType: h.RelationType,
			SemanticsTag: h.SemanticsTag,
			Direction:    h.ReachedVia,
			ViaNode:      h.ViaNodeName,
		})
	}
	if len(panel.Affected) == 0 {
		panel.Note = "node found but no propagating relations — 影响面局限于自身，无上下游依赖"
	}
	return panel, ""
}

// resolveIncidentTopologyNode picks the best topology node to expand from. A
// service node matched by name from the incident labels wins (precise blast
// radius: "who depends on the failing service"); the device's linked node is
// the fallback (coarser — lists co-located services — but always resolvable
// when the incident is device-scoped). Returns (0, reason) when neither
// resolves, so the caller can record a Skipped entry.
func resolveIncidentTopologyNode(ctx context.Context, topo *topologybiz.Usecase, devices *devicebiz.Usecase, inc *alertmodel.Incident, labels, annotations map[string]string) (uint64, string) {
	// 1. Service / container name from labels, then annotations. Prefer an
	//    exact (case-insensitive) node-name match over a substring hit so a
	//    label like name="db" doesn't latch onto "db-replica-2".
	for _, src := range []map[string]string{labels, annotations} {
		for _, k := range topologyServiceLabelKeys {
			name := strings.TrimSpace(src[k])
			if name == "" {
				continue
			}
			nodes, _, err := topo.ListNodes(ctx, topologybiz.NodeListFilter{Q: name, Limit: 5})
			if err != nil || len(nodes) == 0 {
				continue
			}
			picked := nodes[0]
			for _, n := range nodes {
				if strings.EqualFold(n.Name, name) {
					picked = n
					break
				}
			}
			return picked.ID, ""
		}
	}
	// 2. Device's linked node fallback. The device_id COLUMN is authoritative
	//    when set, but device_offline incidents often carry the id only in
	//    labels (column NULL) — resolveIncidentDeviceID handles both so a
	//    host-down incident still reaches its device node and the blast radius
	//    can express "every service deployed_on this host".
	if devices != nil {
		if devID := resolveIncidentDeviceID(inc, labels, annotations); devID != 0 {
			dev, err := devices.Get(ctx, devID)
			if err == nil && dev != nil && dev.NodeID != nil {
				node, nerr := topo.GetNode(ctx, *dev.NodeID)
				if nerr == nil && node != nil {
					return node.ID, ""
				}
			}
		}
	}
	return 0, "no topology node resolved from incident labels or device"
}

// resolveIncidentDeviceID picks the device registry ID (devices.id) for an
// incident: the device_id COLUMN when set, else the first numeric
// topologyDeviceLabelKeys hit in labels then annotations. device_offline rules
// merge the firing series' labels onto the incident but leave the COLUMN NULL,
// so the label fallback is what lets a host-down incident resolve at all.
// Non-numeric values (e.g. a prometheus instance="ongrid:9100" label) are
// ignored rather than mis-parsed.
func resolveIncidentDeviceID(inc *alertmodel.Incident, labels, annotations map[string]string) uint64 {
	if inc != nil && inc.DeviceID != nil && *inc.DeviceID != 0 {
		return *inc.DeviceID
	}
	for _, src := range []map[string]string{labels, annotations} {
		for _, k := range topologyDeviceLabelKeys {
			v := strings.TrimSpace(src[k])
			if v == "" {
				continue
			}
			if parsed, err := strconv.ParseUint(v, 10, 64); err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return 0
}
