DROP TABLE IF EXISTS edge_upgrade_job_items;
DROP TABLE IF EXISTS edge_upgrade_jobs;
ALTER TABLE edges DROP COLUMN last_registered_at;
