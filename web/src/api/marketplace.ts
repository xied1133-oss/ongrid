import { request } from './client';

// Marketplace API client — talks to /v1/marketplace/* (/ N+5a).
// The backend handler lives at internal/manager/server/marketplace/http.go;
// the wire shapes mirror the Go types in internal/manager/biz/marketplace
// (Source / CapabilityDeclaration / InstallResult) and
// internal/manager/model/marketplace (InstalledPack).
//
// Note: model.InstalledPack ships without `json:` tags so its fields go
// over the wire as Go-style PascalCase. We expose them through a normalised
// snake_case TS type by remapping in listInstalledPacks() — easier to keep
// the SPA code aligned with the rest of the API client (settings.ts /
// alerts.ts) which all use snake_case.

export type SourceType = 'local' | 'tarball' | 'git' | 'registry';

export interface InstallSource {
  type: SourceType;
  /** absolute host path; required for type=local. */
  path?: string;
  /** http(s) URL; required for type=tarball|git. */
  url?: string;
  /** optional git ref (branch/tag); only meaningful for type=git. */
  ref?: string;
  /** registry name (e.g. "ongrid-official"); required for type=registry. */
  registry?: string;
  /** pack slug under the registry. */
  pack_id?: string;
  /** semver under (registry, pack_id). */
  version?: string;
}

/** One credential slot a skill declares (requires.credentials[]). The
 *  binding dialog renders one "pick a credential" row per slot. */
export interface CredentialSlotRecord {
  slot: string;
  label: string;
  fields?: string[];
}

export interface SkillCapabilityRecord {
  name: string;
  scope: 'manager' | 'edge';
  edge_capabilities?: Array<Record<string, unknown>>;
  requires: { bins?: string[]; config?: string[]; credentials?: CredentialSlotRecord[] };
  tool_classes: string[];
}

export interface CapabilityDeclaration {
  pack_id: string;
  version: string;
  skills: SkillCapabilityRecord[];
  /** number of agent personas the pack ships (no capability decl yet). */
  agent_count: number;
  /** deduped union across all skills — used by the install-confirm dialog. */
  summary: {
    tool_classes?: string[];
    bins?: string[];
    config_keys?: string[];
    credential_slots?: CredentialSlotRecord[];
  };
}

export interface LoadWarning {
  path: string;
  reason: string;
  code: string;
}

/** SignatureState mirrors model.SignatureState* — */
export type SignatureState = 'verified' | 'unsigned' | 'failed';

export interface InstalledPack {
  id: number;
  pack_id: string;
  display_name: string;
  version: string;
  /** install path family — see Source.SourceLabel(). e.g. "local", "git",
   * "tarball", "<registry-name>". */
  source: string;
  source_url: string;
  install_path: string;
  manifest_sha256: string;
  signature_state: SignatureState | string;
  capabilities: CapabilityDeclaration | null;
  /** operator's slot→credential-name choices (HLD-017). Empty until bound. */
  bindings: Record<string, string>;
  installed_by: number;
  installed_at: string;
  updated_at: string;
}

export interface RegistryEntry {
  name: string;
  url?: string;
  allowed: boolean;
}

/** What POST /install returns: pack metadata + capabilities snapshot +
 *  loader warnings (so the SPA can flag e.g. hooks_dropped before the
 *  user wires the skill into a chat). */
export interface InstallResponse {
  pack: InstalledPack;
  capabilities: CapabilityDeclaration;
  warnings: LoadWarning[];
}

// ---------- internal wire shapes ------------------------------------------

// model.InstalledPack has no json tags, so the wire shape is PascalCase.
// We accept either PascalCase (real server) or snake_case (test fixtures /
// future cleanup) so the SPA keeps working if the backend gets json tags.
type RawInstalledPack = {
  ID?: number;
  id?: number;
  TenantID?: number;
  PackID?: string;
  pack_id?: string;
  DisplayName?: string;
  display_name?: string;
  Version?: string;
  version?: string;
  Source?: string;
  source?: string;
  SourceURL?: string;
  source_url?: string;
  InstallPath?: string;
  install_path?: string;
  ManifestSHA256?: string;
  manifest_sha256?: string;
  SignatureState?: string;
  signature_state?: string;
  CapabilitiesJSON?: string;
  capabilities_json?: string;
  capabilities?: CapabilityDeclaration | null;
  BindingsJSON?: string;
  bindings_json?: string;
  bindings?: Record<string, string>;
  InstalledBy?: number;
  installed_by?: number;
  InstalledAt?: string;
  installed_at?: string;
  UpdatedAt?: string;
  updated_at?: string;
};

function pick<T>(...candidates: Array<T | undefined | null>): T | undefined {
  for (const c of candidates) {
    if (c !== undefined && c !== null) return c;
  }
  return undefined;
}

function parseCapabilities(raw: RawInstalledPack): CapabilityDeclaration | null {
  if (raw.capabilities && typeof raw.capabilities === 'object') {
    return raw.capabilities;
  }
  const json = raw.CapabilitiesJSON ?? raw.capabilities_json;
  if (!json) return null;
  try {
    return JSON.parse(json) as CapabilityDeclaration;
  } catch {
    return null;
  }
}

function parseBindings(raw: RawInstalledPack): Record<string, string> {
  if (raw.bindings && typeof raw.bindings === 'object') return raw.bindings;
  const json = raw.BindingsJSON ?? raw.bindings_json;
  if (!json) return {};
  try {
    const o = JSON.parse(json) as Record<string, string>;
    return o && typeof o === 'object' ? o : {};
  } catch {
    return {};
  }
}

function normalisePack(raw: RawInstalledPack): InstalledPack {
  return {
    id: pick(raw.id, raw.ID) ?? 0,
    pack_id: pick(raw.pack_id, raw.PackID) ?? '',
    display_name: pick(raw.display_name, raw.DisplayName) ?? '',
    version: pick(raw.version, raw.Version) ?? '',
    source: pick(raw.source, raw.Source) ?? '',
    source_url: pick(raw.source_url, raw.SourceURL) ?? '',
    install_path: pick(raw.install_path, raw.InstallPath) ?? '',
    manifest_sha256: pick(raw.manifest_sha256, raw.ManifestSHA256) ?? '',
    signature_state: pick(raw.signature_state, raw.SignatureState) ?? 'unsigned',
    capabilities: parseCapabilities(raw),
    bindings: parseBindings(raw),
    installed_by: pick(raw.installed_by, raw.InstalledBy) ?? 0,
    installed_at: pick(raw.installed_at, raw.InstalledAt) ?? '',
    updated_at: pick(raw.updated_at, raw.UpdatedAt) ?? '',
  };
}

// ---------- public surface ------------------------------------------------

type ListInstalledResp = {
  items?: RawInstalledPack[] | null;
  Items?: RawInstalledPack[] | null;
  total?: number;
  Total?: number;
};

export async function listInstalledPacks(): Promise<InstalledPack[]> {
  const r = await request<ListInstalledResp>('GET', '/marketplace/installed');
  const items = r.items ?? r.Items ?? [];
  return items.map(normalisePack);
}

type InstallRawResp = {
  pack?: RawInstalledPack;
  Pack?: RawInstalledPack;
  capabilities?: CapabilityDeclaration;
  Capabilities?: CapabilityDeclaration;
  warnings?: LoadWarning[];
  Warnings?: LoadWarning[];
};

export async function installPack(src: InstallSource): Promise<InstallResponse> {
  const raw = await request<InstallRawResp>('POST', '/marketplace/install', src);
  const pack = raw.pack ?? raw.Pack;
  const caps = raw.capabilities ?? raw.Capabilities;
  if (!pack || !caps) {
    throw new Error('install response missing pack/capabilities');
  }
  return {
    pack: normalisePack(pack),
    capabilities: caps,
    warnings: raw.warnings ?? raw.Warnings ?? [],
  };
}

/** Persist the operator's slot→credential-name choices for an installed
 *  pack (HLD-017 design-time credential binding). Replaces the whole map;
 *  pass {} to clear. Admin-only on the backend. */
export async function setPackBindings(
  packId: string,
  bindings: Record<string, string>,
): Promise<void> {
  await request<{ ok: boolean }>(
    'PUT',
    `/marketplace/installed/${encodeURIComponent(packId)}/bindings`,
    { bindings },
  );
}

/** Install a pack from a browser file upload (zip / tar.gz). The archive is
 *  extracted server-side and installed like a local-dir install. Admin-only. */
export async function uploadPack(file: File): Promise<InstallResponse> {
  const fd = new FormData();
  fd.append('file', file);
  const raw = await request<InstallRawResp>('POST', '/marketplace/upload', fd);
  const pack = raw.pack ?? raw.Pack;
  const caps = raw.capabilities ?? raw.Capabilities;
  if (!pack || !caps) {
    throw new Error('upload response missing pack/capabilities');
  }
  return {
    pack: normalisePack(pack),
    capabilities: caps,
    warnings: raw.warnings ?? raw.Warnings ?? [],
  };
}

export async function uninstallPack(packId: string): Promise<void> {
  await request<void>('DELETE', `/marketplace/installed/${encodeURIComponent(packId)}`);
}

type RegistriesResp = { items?: RegistryEntry[] | null; Items?: RegistryEntry[] | null };

export async function listRegistries(): Promise<RegistryEntry[]> {
  const r = await request<RegistriesResp>('GET', '/marketplace/registries');
  return r.items ?? r.Items ?? [];
}

// ---------- error helpers (for UI toast wording) --------------------------

export type MarketplaceErrorKind =
  | 'unauthorized'
  | 'forbidden'
  | 'conflict'
  | 'invalid'
  | 'not-found'
  | 'network'
  | 'internal';

/** classifyError maps an ApiError or generic error to a stable kind so the
 *  install/uninstall flows can render localised toast copy. */
export function classifyError(err: unknown): MarketplaceErrorKind {
  if (err && typeof err === 'object') {
    const e = err as { status?: number; code?: string };
    if (e.status === 401) return 'unauthorized';
    if (e.status === 403) return 'forbidden';
    if (e.status === 409) return 'conflict';
    if (e.status === 400 || e.code === 'invalid-argument') return 'invalid';
    if (e.status === 404) return 'not-found';
    if (e.status === 0) return 'network';
  }
  return 'internal';
}
