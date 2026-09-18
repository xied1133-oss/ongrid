package tools

import (
	"context"
	"time"

	devicebiz "github.com/ongridio/ongrid/internal/manager/biz/device"
	edgebiz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	devicemodel "github.com/ongridio/ongrid/internal/manager/model/device"
	edgemodel "github.com/ongridio/ongrid/internal/manager/model/edge"
)

// DeviceResolver resolves a device_id to its host edge_id via the
// edge_devices junction. It is the shared seam used by every
// ScopeHost tool (host_files, host_load, host_processes, et al.) and
// by skill_bridge — every place that needs to translate a chat-input
// device_id (the same id the SPA's @-mention chip uses, the same id
// labelled on Prom samples) into the tunnel-addressable edge_id.
//
// PR-9 of introduces this as a single, testable helper. The
// previous implementations were copy-pasted between
// host_files_basetool.go (usecaseDeviceResolver) and skill_bridge.go
// (Registry.resolveEdgeForDeviceID); they are now thin wrappers
// around DeviceResolver so a single change to the resolution rule
// (e.g. "prefer junction, then row, then legacy") affects every tool
// uniformly.
//
// Resolution rules (rule 1 refines the legacy behaviour; 2–4 unchanged):
//
//  1. Junction lookup, preferring a LIVE edge. Enumerate every type=host
//     junction for the device and keep the ones whose edge row is
//     status=online — freshest last_seen wins, ties broken by the largest
//     junction id to match the legacy id-DESC rule. This matters because
//     one device can carry several host junctions (e.g. a stale credential
//     edge that never connected plus the real running agent); the legacy
//     blind id-DESC pick could lock host commands onto the dead edge while
//     a healthy one existed — the exact split that made get_host_load fail
//     with "edge not online" even though the device page showed it online.
//     When no host edge is online (or the edges usecase is nil, so status
//     is unknowable) fall back to devices.Links().LookupEdgeForDevice
//     (id-DESC), preserving the old result.
//  2. Device-row presence check. If the device row exists but no
//     junction link is present, return (0, nil) so the caller can
//     surface a clear "device has no host link" error rather than
//     silently routing to a stranger edge.
//  3. Legacy fallback. Treat the input as a raw edge_id (edges.Get).
//     Found → return. This preserves back-compat with prompts that
//     pre-date the device split and still refer to edge ids directly.
//  4. Otherwise (0, nil).
type DeviceResolver interface {
	// ResolveEdgeID resolves a device_id (or legacy edge_id) to a
	// host edge_id. Returns (0, nil) when the device exists but has
	// no host-edge link AND no fallback edge row matches; callers
	// should surface that as a friendly "no host link" error.
	ResolveEdgeID(ctx context.Context, deviceID uint64) (uint64, error)
}

// junctionDeviceResolver is the production implementation backed by
// the device + edge usecases. Keep the struct unexported so callers
// reach it via NewDeviceResolver — we may swap the underlying repo
// (e.g. add a cache) without touching call sites.
type junctionDeviceResolver struct {
	devices *devicebiz.Usecase
	edges   *edgebiz.Usecase
}

// NewDeviceResolver builds the production DeviceResolver from the
// device + edge usecases. Either may be nil; the resolver degrades
// gracefully (a nil devices usecase skips the junction lookup, a nil
// edges usecase skips the legacy fallback).
func NewDeviceResolver(devices *devicebiz.Usecase, edges *edgebiz.Usecase) DeviceResolver {
	return junctionDeviceResolver{devices: devices, edges: edges}
}

// ResolveEdgeID implements DeviceResolver. See package comment for
// the 4-step rule.
func (r junctionDeviceResolver) ResolveEdgeID(ctx context.Context, deviceID uint64) (uint64, error) {
	if deviceID == 0 {
		return 0, nil
	}
	if r.devices != nil {
		if links := r.devices.Links(); links != nil {
			// Prefer a LIVE host edge over the raw id-DESC pick (rule 1).
			// A device can carry a stale never-connected edge junction
			// alongside the real running agent; blindly taking the largest
			// junction id could route host commands to the dead edge.
			if eid := r.pickOnlineHostEdge(ctx, links, deviceID); eid != 0 {
				return eid, nil
			}
			eid, err := links.LookupEdgeForDevice(ctx, deviceID, devicemodel.EdgeDeviceRelationHost)
			if err == nil && eid != 0 {
				return eid, nil
			}
			// err != nil here is treated as "not linked" — the
			// resolver disambiguates routing, not surfaces DB
			// errors. The caller distinguishes 0 from "missing".
		}
		if dev, err := r.devices.Get(ctx, deviceID); err == nil && dev != nil {
			// Device row exists but no junction → no fallback.
			return 0, nil
		}
	}
	if r.edges != nil {
		if edge, err := r.edges.Get(ctx, deviceID); err == nil && edge != nil {
			return edge.ID, nil
		}
	}
	return 0, nil
}

// edgeHostCandidate is one type=host junction annotated with the edge
// liveness fields the picker needs. junctionID preserves the legacy
// id-DESC tie-break so behaviour is unchanged when candidates are
// otherwise equal.
type edgeHostCandidate struct {
	edgeID     uint64
	junctionID uint64
	online     bool
	lastSeen   time.Time
}

// pickOnlineHostEdge enumerates every type=host junction for deviceID,
// reads each edge row's status, and returns the best ONLINE candidate's
// edge_id. It returns 0 — letting the caller fall back to the legacy
// id-DESC lookup — when the device has no online host edge, when the
// edges usecase is nil (status unknowable), or when no junction/edge rows
// resolve. Gathering is deliberately thin; the selection rule lives in the
// pure pickBestHostEdge so it can be unit-tested without DB fakes.
func (r junctionDeviceResolver) pickOnlineHostEdge(ctx context.Context, links devicebiz.EdgeDeviceRepo, deviceID uint64) uint64 {
	if r.edges == nil {
		return 0
	}
	rows, err := links.ListEdgesForDevice(ctx, deviceID)
	if err != nil || len(rows) == 0 {
		return 0
	}
	cands := make([]edgeHostCandidate, 0, len(rows))
	for _, row := range rows {
		if row == nil || row.Type != devicemodel.EdgeDeviceRelationHost || row.EdgeID == 0 {
			continue
		}
		edge, gerr := r.edges.Get(ctx, row.EdgeID)
		if gerr != nil || edge == nil {
			continue
		}
		c := edgeHostCandidate{
			edgeID:     row.EdgeID,
			junctionID: row.ID,
			online:     edge.Status == edgemodel.StatusOnline,
		}
		if edge.LastSeenAt != nil {
			c.lastSeen = *edge.LastSeenAt
		}
		cands = append(cands, c)
	}
	return pickBestHostEdge(cands)
}

// pickBestHostEdge is the pure selection rule: among online candidates
// pick the freshest last_seen, tie-broken by the largest junction id
// (matching the legacy id-DESC preference). Offline candidates are
// ignored; if none is online it returns 0 so the caller degrades to the
// legacy lookup rather than routing to a dead edge on purpose.
func pickBestHostEdge(cands []edgeHostCandidate) uint64 {
	var best edgeHostCandidate
	found := false
	for _, c := range cands {
		if !c.online {
			continue
		}
		if !found || betterHostEdge(c, best) {
			best, found = c, true
		}
	}
	if !found {
		return 0
	}
	return best.edgeID
}

// betterHostEdge orders two online candidates: a later last_seen wins;
// equal timestamps fall back to the larger junction id. A zero last_seen
// never outranks a real one because After() is false for it.
func betterHostEdge(cand, best edgeHostCandidate) bool {
	if !cand.lastSeen.Equal(best.lastSeen) {
		return cand.lastSeen.After(best.lastSeen)
	}
	return cand.junctionID > best.junctionID
}
