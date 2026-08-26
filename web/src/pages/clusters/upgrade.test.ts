import { describe, expect, it } from "vitest";
import type { Device } from "@/api/devices";
import type { Edge } from "@/api/edges";
import {
  buildClusterUpgradePlan,
  chunkUpgradeTargets,
  evaluateUpgradeConvergence,
  normalizeUpgradePackageArch,
  selectClusterHostEdges,
  type ClusterUpgradeTarget,
} from "./upgrade";

function edge(id: number, deviceID: number, status: Edge["status"]): Edge {
  return {
    id,
    device_id: deviceID,
    name: `edge-${id}`,
    status,
    roles: [],
    access_key_id: `ak-${id}`,
    last_seen_at: `2026-07-31T00:00:0${id}Z`,
  };
}

describe("cluster upgrade planning", () => {
  it("classifies eligible, offline, unlinked, and unsupported members", () => {
    const devices: Device[] = [
      { id: 1, name: "amd64", online: true, os: "linux", arch: "x86_64" },
      { id: 2, name: "offline", online: false, os: "linux", arch: "amd64" },
      { id: 3, name: "unlinked", online: true, os: "linux", arch: "amd64" },
      {
        id: 4,
        name: "windows",
        online: true,
        os: "windows",
        arch: "amd64",
      },
      { id: 5, name: "arm64", online: true, os: "linux", arch: "aarch64" },
    ];
    const edges = new Map<number, Edge>([
      [1, edge(11, 1, "online")],
      [2, edge(12, 2, "online")],
      [4, edge(14, 4, "online")],
      [5, edge(15, 5, "online")],
    ]);

    const plan = buildClusterUpgradePlan(devices, edges);

    expect(
      plan.targets.map((target) => [target.device.id, target.packageArch]),
    ).toEqual([
      [1, "linux-amd64"],
      [5, "linux-arm64"],
    ]);
    expect(plan.offline.map((device) => device.id)).toEqual([2]);
    expect(plan.unlinked.map((device) => device.id)).toEqual([3]);
    expect(plan.unsupported.map((device) => device.id)).toEqual([4]);
  });

  it("normalizes supported Linux architecture aliases", () => {
    expect(normalizeUpgradePackageArch({ os: "Linux", arch: "amd64" })).toBe(
      "linux-amd64",
    );
    expect(normalizeUpgradePackageArch({ arch: "aarch64" })).toBe(
      "linux-arm64",
    );
    expect(
      normalizeUpgradePackageArch({ os: "darwin", arch: "arm64" }),
    ).toBeNull();
    expect(normalizeUpgradePackageArch({ os: "linux" })).toBeNull();
  });

  it("selects the online and most recently seen Edge for each device", () => {
    const selected = selectClusterHostEdges([
      edge(1, 9, "offline"),
      edge(2, 9, "online"),
      edge(3, 9, "online"),
    ]);

    expect(selected.get(9)?.id).toBe(3);
  });

  it("skips the target version unless force reinstall is enabled", () => {
    const device: Device = {
      id: 1,
      name: "host",
      online: true,
      os: "linux",
      arch: "amd64",
    };
    const current = { ...edge(11, 1, "online"), agent_version: "0.10.2" };
    const bundles = [
      {
        arch: "linux-amd64" as const,
        version: "v0.10.2",
        available: true,
      },
    ];

    const normal = buildClusterUpgradePlan([device], new Map([[1, current]]), {
      targetVersion: "v0.10.2",
      bundles,
      enforceBundleAvailability: true,
    });
    const forced = buildClusterUpgradePlan([device], new Map([[1, current]]), {
      targetVersion: "v0.10.2",
      bundles,
      enforceBundleAvailability: true,
      forceReinstall: true,
    });

    expect(normal.upToDate).toHaveLength(1);
    expect(normal.targets).toHaveLength(0);
    expect(forced.upToDate).toHaveLength(0);
    expect(forced.targets).toHaveLength(1);
  });

  it("blocks only devices whose architecture artifact is unavailable", () => {
    const amd64: Device = {
      id: 1,
      name: "amd64",
      online: true,
      os: "linux",
      arch: "amd64",
    };
    const arm64: Device = {
      id: 2,
      name: "arm64",
      online: true,
      os: "linux",
      arch: "arm64",
    };
    const plan = buildClusterUpgradePlan(
      [amd64, arm64],
      new Map([
        [1, edge(11, 1, "online")],
        [2, edge(12, 2, "online")],
      ]),
      {
        targetVersion: "v1",
        enforceBundleAvailability: true,
        bundles: [
          { arch: "linux-amd64", version: "v1", available: true },
          { arch: "linux-arm64", version: "v1", available: false },
        ],
      },
    );

    expect(plan.targets.map((target) => target.device.id)).toEqual([1]);
    expect(plan.missingBundle.map((target) => target.device.id)).toEqual([2]);
  });

  it("requires a new registration and the target version for convergence", () => {
    const original = { last_registered_at: "2026-07-31T01:00:00Z" };

    expect(
      evaluateUpgradeConvergence(
        original,
        {
          status: "online",
          last_registered_at: "2026-07-31T01:00:00Z",
          agent_version: "v2",
        },
        "v2",
      ),
    ).toBe("waiting_reconnect");
    expect(
      evaluateUpgradeConvergence(
        original,
        {
          status: "online",
          last_registered_at: "2026-07-31T01:01:00Z",
          agent_version: "v1",
        },
        "v2",
      ),
    ).toBe("version_mismatch");
    expect(
      evaluateUpgradeConvergence(
        original,
        {
          status: "online",
          last_registered_at: "2026-07-31T01:01:00Z",
          agent_version: "2",
        },
        "v2",
      ),
    ).toBe("completed");
  });

  it("chunks requests at the backend batch limit", () => {
    const target = {
      device: { id: 1, name: "host", online: true },
      edge: edge(1, 1, "online"),
      packageArch: "linux-amd64",
    } satisfies ClusterUpgradeTarget;
    const targets = Array.from({ length: 1001 }, (_, index) => ({
      ...target,
      device: { ...target.device, id: index + 1 },
      edge: { ...target.edge, id: index + 1, device_id: index + 1 },
    }));

    expect(chunkUpgradeTargets(targets).map((chunk) => chunk.length)).toEqual([
      500, 500, 1,
    ]);
  });
});
