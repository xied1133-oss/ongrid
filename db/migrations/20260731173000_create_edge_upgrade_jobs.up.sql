ALTER TABLE edges
    ADD COLUMN last_registered_at DATETIME(3) NULL AFTER last_seen_at;

CREATE TABLE IF NOT EXISTS edge_upgrade_jobs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    cluster_node_id BIGINT UNSIGNED NULL,
    target_version VARCHAR(32) NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL DEFAULT 'queued',
    force_reinstall BOOLEAN NOT NULL DEFAULT FALSE,
    batch_size INT NOT NULL DEFAULT 10,
    current_batch INT NOT NULL DEFAULT 0,
    total_batches INT NOT NULL DEFAULT 0,
    total INT NOT NULL DEFAULT 0,
    succeeded INT NOT NULL DEFAULT 0,
    failed INT NOT NULL DEFAULT 0,
    skipped INT NOT NULL DEFAULT 0,
    pending INT NOT NULL DEFAULT 0,
    created_by BIGINT UNSIGNED NULL,
    started_at DATETIME(3) NULL,
    finished_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3) NULL,
    PRIMARY KEY (id),
    KEY idx_edge_upgrade_jobs_cluster_node_id (cluster_node_id),
    KEY idx_edge_upgrade_jobs_status (status),
    KEY idx_edge_upgrade_jobs_deleted_at (deleted_at)
);

CREATE TABLE IF NOT EXISTS edge_upgrade_job_items (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    job_id BIGINT UNSIGNED NOT NULL,
    edge_id BIGINT UNSIGNED NOT NULL,
    device_id BIGINT UNSIGNED NULL,
    edge_name VARCHAR(128) NOT NULL DEFAULT '',
    device_name VARCHAR(255) NOT NULL DEFAULT '',
    arch VARCHAR(32) NOT NULL DEFAULT '',
    from_version VARCHAR(32) NOT NULL DEFAULT '',
    target_version VARCHAR(32) NOT NULL DEFAULT '',
    batch_number INT NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL DEFAULT 'queued',
    attempt INT NOT NULL DEFAULT 0,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    error_message VARCHAR(1024) NOT NULL DEFAULT '',
    observed_version VARCHAR(32) NOT NULL DEFAULT '',
    baseline_registered_at DATETIME(3) NULL,
    observed_registered_at DATETIME(3) NULL,
    verification_deadline_at DATETIME(3) NULL,
    started_at DATETIME(3) NULL,
    finished_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY idx_edge_upgrade_job_edge (job_id, edge_id),
    KEY idx_edge_upgrade_job_items_edge_id (edge_id),
    KEY idx_edge_upgrade_job_items_batch_number (batch_number),
    CONSTRAINT fk_edge_upgrade_job_items_job
        FOREIGN KEY (job_id) REFERENCES edge_upgrade_jobs(id) ON DELETE CASCADE
);
