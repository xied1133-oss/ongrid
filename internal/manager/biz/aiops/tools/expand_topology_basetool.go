package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/ongridio/ongrid/internal/manager/biz/aiops/tools/basetool"
	devicebiz "github.com/ongridio/ongrid/internal/manager/biz/device"
	topologybiz "github.com/ongridio/ongrid/internal/manager/biz/topology"
)

const (
	ToolNameExpandTopology = "expand_topology"

	// Defaults chosen to match what the AIOps reasoning loop actually
	// needs in practice: 2 hops is enough to surface "this service +
	// its cluster + the devices that cluster runs on" without spamming
	// the prompt; cap at 5 to bound BFS even when the operator passes
	// something silly.
	expandTopologyDefaultDepth = 2
	expandTopologyMaxDepth     = 5
	expandTopologyCallTimeout  = 8 * time.Second
)

const expandTopologyDescription = "Walk the business topology graph outward from a starting node (or a device's node). " +
	"Returns every reachable node with the hop count, the relation type that led there, and that relation's AIOps semantics tag. " +
	"Default depth=2, only follows propagating edges (depends_on / deployed_on / routes_to) so the result is the failure blast-radius."

const expandTopologyWhenToUse = "When the user asks 'what depends on X', 'what would break if X dies', 'show me the blast radius of node X', " +
	"or as a follow-up after query_incidents / get_incident_detail to figure out which other services are affected by an alert on a single device. " +
	"NOT for the deployment-level overview (use get_topology) and NOT for the flat node list (use query_devices)."

var expandTopologySchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "node_id": {
      "type": "integer",
      "description": "Topology node id to BFS from. Mutually exclusive with device_id; one of the two is required."
    },
    "device_id": {
      "type": "integer",
      "description": "Resolve a Device's linked node and BFS from there. Convenience for when the LLM has a device_id from query_devices but not yet a node_id."
    },
    "depth": {
      "type": "integer",
      "description": "Max BFS hops. Default 2; cap 5.",
      "default": 2
    },
    "only_propagating": {
      "type": "boolean",
      "description": "When true (default), only walk relations whose semantics tag drives failure propagation (hard_dep / runtime_dep / traffic). When false, walks every relation including annotation / observation.",
      "default": true
    },
    "direction": {
      "type": "string",
      "enum": ["both", "downstream", "upstream"],
      "description": "downstream = follow propagation direction (failure on src reaches these); upstream = inverse (these can cause failure on src). Default both for a symmetric blast radius.",
      "default": "both"
    }
  }
}`)

// expandTopologyArgs mirrors expandTopologySchema. The LLM marshals
// either node_id or device_id; we resolve the latter via the device
// usecase's Get + the Device.NodeID column populated by PR-2.
// ID/depth 走容错类型：量化小模型常把数字传成字符串（见
// lenient_args.go 实证），严格 unmarshal 会整轮报废。
type expandTopologyArgs struct {
	NodeID          LenientID  `json:"node_id,omitempty"`
	DeviceID        LenientID  `json:"device_id,omitempty"`
	Depth           LenientInt `json:"depth,omitempty"`
	OnlyPropagating *bool      `json:"only_propagating,omitempty"`
	Direction       string     `json:"direction,omitempty"`
}

// expandTopologyHit is one reachable node + the path metadata the LLM
// needs to reason about impact. Kept flat (no nested struct per
// neighbor) so the tool output stays cheap to embed in the prompt.
type expandTopologyHit struct {
	NodeID       uint64 `json:"node_id"`
	NodeName     string `json:"node_name"`
	NodeType     string `json:"node_type"`
	Hops         int    `json:"hops"`
	RelationType string `json:"relation_type,omitempty"`
	SemanticsTag string `json:"semantics_tag,omitempty"`
	Propagates   bool   `json:"propagates_failure"`
	ReachedVia   string `json:"reached_via,omitempty"` // "downstream" | "upstream"
	ViaNodeID    uint64 `json:"via_node_id,omitempty"`
	ViaNodeName  string `json:"via_node_name,omitempty"`
}

type expandTopologyResult struct {
	Center expandTopologyHit   `json:"center"`
	Hops   int                 `json:"max_hops"`
	Count  int                 `json:"reachable_count"`
	Hits   []expandTopologyHit `json:"reachable"`
	// Note tells the LLM about silent fallbacks (depth clamped, no
	// propagating edges found, etc.) without falling back to error
	// shape — the tool's output is structurally always valid.
	Note string `json:"note,omitempty"`
}

// ExpandTopologyTool is the BaseTool wrapper. Gated on topology UC
// being wired; device resolution is best-effort (nil device UC = the
// device_id path errors clearly).
type ExpandTopologyTool struct {
	topology *topologybiz.Usecase
	devices  *devicebiz.Usecase
	log      *slog.Logger
}

func NewExpandTopologyTool(topology *topologybiz.Usecase, devices *devicebiz.Usecase, log *slog.Logger) *ExpandTopologyTool {
	if log == nil {
		log = slog.Default()
	}
	return &ExpandTopologyTool{topology: topology, devices: devices, log: log}
}

func (t *ExpandTopologyTool) Info(_ context.Context) (*basetool.ToolInfo, error) {
	return &basetool.ToolInfo{
		Name:        ToolNameExpandTopology,
		Description: expandTopologyDescription,
		WhenToUse:   expandTopologyWhenToUse,
		Parameters:  expandTopologySchema,
		Class:       "read",
	}, nil
}

func (t *ExpandTopologyTool) InvokableRun(ctx context.Context, argsJSON string, _ ...basetool.InvokeOption) (string, error) {
	if t.topology == nil {
		return "", fmt.Errorf("expand_topology: topology usecase not configured")
	}
	var in expandTopologyArgs
	if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
		return "", fmt.Errorf("expand_topology: bad args: %w", err)
	}
	if in.NodeID == 0 && in.DeviceID == 0 {
		return "", fmt.Errorf("expand_topology: one of node_id or device_id is required")
	}
	if in.Depth <= 0 {
		in.Depth = expandTopologyDefaultDepth
	}
	clampedDepth := false
	if in.Depth > expandTopologyMaxDepth {
		in.Depth = expandTopologyMaxDepth
		clampedDepth = true
	}
	onlyPropagating := true
	if in.OnlyPropagating != nil {
		onlyPropagating = *in.OnlyPropagating
	}
	direction := in.Direction
	if direction == "" {
		direction = "both"
	}
	if direction != "both" && direction != "downstream" && direction != "upstream" {
		return "", fmt.Errorf("expand_topology: direction must be both|downstream|upstream")
	}

	callCtx, cancel := context.WithTimeout(ctx, expandTopologyCallTimeout)
	defer cancel()

	// Resolve start node — device_id path needs Device.NodeID populated
	// (PR-2 backfill takes care of legacy rows; new registers fill it
	// via NodeMirror).
	startID := uint64(in.NodeID)
	if startID == 0 {
		if t.devices == nil {
			return "", fmt.Errorf("expand_topology: device_id supplied but device usecase not configured")
		}
		dev, err := t.devices.Get(callCtx, uint64(in.DeviceID))
		if err != nil {
			return "", fmt.Errorf("expand_topology: resolve device %d: %w", in.DeviceID, err)
		}
		if dev.NodeID == nil {
			return "", fmt.Errorf("expand_topology: device %d has no linked topology node (node_id is NULL — topology.Migrate will backfill on next boot)", in.DeviceID)
		}
		startID = *dev.NodeID
	}

	// Blast-radius BFS is shared with correlate_incident's topology_impact
	// panel (topology_impact.go) so the LLM-driven tool and the deterministic
	// panel can never disagree on propagation semantics for the same node.
	center, hits, err := expandBlastRadius(callCtx, t.topology, startID, int(in.Depth), onlyPropagating, direction)
	if err != nil {
		return "", fmt.Errorf("expand_topology: %w", err)
	}

	result := expandTopologyResult{
		Center: expandTopologyHit{
			NodeID:   center.ID,
			NodeName: center.Name,
			NodeType: center.Type,
		},
		Hops:  int(in.Depth),
		Count: len(hits),
		Hits:  hits,
	}
	if clampedDepth {
		result.Note = "depth clamped to max=5"
	}
	if len(hits) == 0 && onlyPropagating {
		result.Note = "no propagating relations reachable — try only_propagating=false to include observation / annotation edges"
	}
	body, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("expand_topology: marshal: %w", err)
	}
	return string(body), nil
}

func sortHitsByHopsThenName(hits []expandTopologyHit) {
	// Simple insertion sort — len(hits) is typically <50.
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0; j-- {
			a := hits[j-1]
			b := hits[j]
			if a.Hops > b.Hops || (a.Hops == b.Hops && a.NodeName > b.NodeName) {
				hits[j-1], hits[j] = b, a
				continue
			}
			break
		}
	}
}
