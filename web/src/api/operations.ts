import { request } from './client';

export type Operation = {
  id: string;
  kind: string;
  state: string;
  title: string;
  summary?: string;
  detail_url?: string;
  actions_json?: string;
};

export type OperationArtifact = {
  id: string;
  operation_id: string;
  kind: string;
  title: string;
  url: string;
  metadata_json?: string;
  created_at?: string;
};

export type OperationDetail = {
  operation: Operation;
  artifacts?: OperationArtifact[];
};

export async function getOperation(id: string) {
  const detail = await request<unknown>('GET', `/operations/${encodeURIComponent(id)}`);
  return normalizeOperationDetail(detail);
}

export async function executeOperationAction(id: string, action: string) {
  const operation = await request<unknown>('POST', `/operations/${encodeURIComponent(id)}/actions/${encodeURIComponent(action)}`, {});
  return normalizeOperation(operation);
}

function normalizeOperationDetail(value: unknown): OperationDetail {
  if (!value || typeof value !== 'object') {
    return { operation: normalizeOperation(null), artifacts: [] };
  }
  const record = value as Record<string, unknown>;
  const artifacts = Array.isArray(record.artifacts ?? record.Artifacts)
    ? (record.artifacts ?? record.Artifacts) as unknown[]
    : [];
  return {
    operation: normalizeOperation(record.operation ?? record.Operation),
    artifacts: artifacts.map(normalizeOperationArtifact),
  };
}

function normalizeOperation(value: unknown): Operation {
  const record = value && typeof value === 'object' ? value as Record<string, unknown> : {};
  return {
    id: stringField(record, 'id', 'ID'),
    kind: stringField(record, 'kind', 'Kind'),
    state: stringField(record, 'state', 'State'),
    title: stringField(record, 'title', 'Title'),
    summary: optionalStringField(record, 'summary', 'Summary'),
    detail_url: optionalStringField(record, 'detail_url', 'DetailURL'),
    actions_json: optionalStringField(record, 'actions_json', 'ActionsJSON'),
  };
}

function normalizeOperationArtifact(value: unknown): OperationArtifact {
  const record = value && typeof value === 'object' ? value as Record<string, unknown> : {};
  return {
    id: stringField(record, 'id', 'ID'),
    operation_id: stringField(record, 'operation_id', 'OperationID'),
    kind: stringField(record, 'kind', 'Kind'),
    title: stringField(record, 'title', 'Title'),
    url: stringField(record, 'url', 'URL'),
    metadata_json: optionalStringField(record, 'metadata_json', 'MetadataJSON'),
    created_at: optionalStringField(record, 'created_at', 'CreatedAt'),
  };
}

function stringField(record: Record<string, unknown>, primary: string, legacy: string) {
  const value = record[primary] ?? record[legacy];
  return typeof value === 'string' ? value : '';
}

function optionalStringField(record: Record<string, unknown>, primary: string, legacy: string) {
  const value = record[primary] ?? record[legacy];
  return typeof value === 'string' ? value : undefined;
}
