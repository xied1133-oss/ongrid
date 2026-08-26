import type { Device } from "@/api/devices";
import type { Edge, EdgeUpgradeBundle } from "@/api/edges";

export const CLUSTER_UPGRADE_BATCH_SIZE = 500;

export type ClusterUpgradeTarget = {
  device: Device;
  edge: Edge;
  packageArch: "linux-amd64" | "linux-arm64";
};

export type ClusterUpgradePlan = {
  targets: ClusterUpgradeTarget[];
  upToDate: ClusterUpgradeTarget[];
  missingBundle: ClusterUpgradeTarget[];
  offline: Device[];
  unlinked: Device[];
  unsupported: Device[];
};

export type ClusterUpgradePlanOptions = {
  targetVersion?: string;
  bundles?: EdgeUpgradeBundle[];
  enforceBundleAvailability?: boolean;
  forceReinstall?: boolean;
};

export type UpgradeConvergence =
  | "waiting_reconnect"
  | "completed"
  | "version_mismatch";

/**
 * A device may temporarily have more than one host Edge identity. Prefer the
 * online identity, then the one with the newest heartbeat, matching the device
 * page's selection rule.
 */
export function selectClusterHostEdges(edges: Edge[]): Map<number, Edge> {
  const out = new Map<number, Edge>();
  for (const edge of edges) {
    const deviceID = edge.device_id;
    if (!deviceID) continue;
    const current = out.get(deviceID);
    if (!current || isBetterEdge(edge, current)) out.set(deviceID, edge);
  }
  return out;
}

export function buildClusterUpgradePlan(
  devices: Device[],
  edgesByDeviceID: ReadonlyMap<number, Edge>,
  options: ClusterUpgradePlanOptions = {},
): ClusterUpgradePlan {
  const plan: ClusterUpgradePlan = {
    targets: [],
    upToDate: [],
    missingBundle: [],
    offline: [],
    unlinked: [],
    unsupported: [],
  };

  for (const device of devices) {
    const edge = edgesByDeviceID.get(device.id);
    if (!edge) {
      plan.unlinked.push(device);
      continue;
    }
    if (device.online !== true || edge.status !== "online") {
      plan.offline.push(device);
      continue;
    }
    const packageArch = normalizeUpgradePackageArch(device);
    if (!packageArch) {
      plan.unsupported.push(device);
      continue;
    }
    const target = { device, edge, packageArch } satisfies ClusterUpgradeTarget;
    if (
      !options.forceReinstall &&
      options.targetVersion &&
      versionsEqual(edge.agent_version, options.targetVersion)
    ) {
      plan.upToDate.push(target);
      continue;
    }
    if (
      options.enforceBundleAvailability &&
      !hasAvailableBundle(options.bundles ?? [], packageArch, options.targetVersion)
    ) {
      plan.missingBundle.push(target);
      continue;
    }
    plan.targets.push(target);
  }

  return plan;
}

export function evaluateUpgradeConvergence(
  original: Pick<Edge, "last_registered_at">,
  current: Pick<Edge, "status" | "last_registered_at" | "agent_version"> | undefined,
  targetVersion: string,
): UpgradeConvergence {
  if (
    !current ||
    current.status !== "online" ||
    !isNewRegistration(original.last_registered_at, current.last_registered_at)
  ) {
    return "waiting_reconnect";
  }
  if (targetVersion && !versionsEqual(current.agent_version, targetVersion)) {
    return "version_mismatch";
  }
  return "completed";
}

export function versionsEqual(left?: string | null, right?: string | null) {
  const normalize = (value?: string | null) =>
    value
      ?.trim()
      .toLocaleLowerCase()
      .replace(/^v(?=\d)/, "") ?? "";
  const normalizedLeft = normalize(left);
  const normalizedRight = normalize(right);
  return normalizedLeft !== "" && normalizedLeft === normalizedRight;
}

export function normalizeUpgradePackageArch(
  device: Pick<Device, "os" | "arch">,
): ClusterUpgradeTarget["packageArch"] | null {
  const os = device.os?.trim().toLocaleLowerCase();
  if (os && os !== "linux") return null;

  switch (device.arch?.trim().toLocaleLowerCase()) {
    case "amd64":
    case "x86_64":
    case "x64":
    case "linux-amd64":
    case "linux/amd64":
      return "linux-amd64";
    case "arm64":
    case "aarch64":
    case "linux-arm64":
    case "linux/arm64":
      return "linux-arm64";
    default:
      return null;
  }
}

export function groupUpgradeTargets(
  targets: ClusterUpgradeTarget[],
): Map<ClusterUpgradeTarget["packageArch"], ClusterUpgradeTarget[]> {
  const groups = new Map<
    ClusterUpgradeTarget["packageArch"],
    ClusterUpgradeTarget[]
  >();
  for (const target of targets) {
    const group = groups.get(target.packageArch) ?? [];
    group.push(target);
    groups.set(target.packageArch, group);
  }
  return groups;
}

export function chunkUpgradeTargets(
  targets: ClusterUpgradeTarget[],
  size = CLUSTER_UPGRADE_BATCH_SIZE,
): ClusterUpgradeTarget[][] {
  if (!Number.isInteger(size) || size <= 0) {
    throw new Error("batch size must be a positive integer");
  }
  const out: ClusterUpgradeTarget[][] = [];
  for (let start = 0; start < targets.length; start += size) {
    out.push(targets.slice(start, start + size));
  }
  return out;
}

function isBetterEdge(candidate: Edge, current: Edge): boolean {
  if (candidate.status !== current.status) {
    return candidate.status === "online";
  }
  return edgeSeenAt(candidate) > edgeSeenAt(current);
}

function edgeSeenAt(edge: Edge): number {
  if (!edge.last_seen_at) return 0;
  const timestamp = Date.parse(edge.last_seen_at);
  return Number.isFinite(timestamp) ? timestamp : 0;
}

function hasAvailableBundle(
  bundles: EdgeUpgradeBundle[],
  arch: ClusterUpgradeTarget["packageArch"],
  targetVersion?: string,
) {
  return bundles.some(
    (bundle) =>
      bundle.arch === arch &&
      bundle.available &&
      (!targetVersion || versionsEqual(bundle.version, targetVersion)),
  );
}

function isNewRegistration(
  previous?: string | null,
  current?: string | null,
) {
  if (!current) return false;
  if (!previous) return true;
  const previousAt = Date.parse(previous);
  const currentAt = Date.parse(current);
  if (Number.isFinite(previousAt) && Number.isFinite(currentAt)) {
    return currentAt > previousAt;
  }
  return current !== previous;
}
