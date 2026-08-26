ALTER TABLE k8s_workloads
    ADD COLUMN active_replicas BIGINT NOT NULL DEFAULT 0 AFTER ready_replicas,
    ADD COLUMN failed_replicas BIGINT NOT NULL DEFAULT 0 AFTER active_replicas,
    ADD COLUMN is_terminal_failure BOOLEAN NOT NULL DEFAULT FALSE AFTER failed_replicas,
    ADD COLUMN owner_kind VARCHAR(64) NOT NULL DEFAULT '' AFTER is_terminal_failure,
    ADD COLUMN owner_name VARCHAR(255) NOT NULL DEFAULT '' AFTER owner_kind,
    ADD COLUMN owner_uid VARCHAR(128) NOT NULL DEFAULT '' AFTER owner_name,
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 0 AFTER owner_uid,
    ADD COLUMN resource_created_at DATETIME(3) NULL AFTER revision,
    ADD INDEX idx_k8s_workloads_owner (cluster_id, owner_kind, owner_uid);
