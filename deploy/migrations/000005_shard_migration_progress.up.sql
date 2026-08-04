CREATE TABLE IF NOT EXISTS shard_migration_progress (
    logical_shard_id INT UNSIGNED NOT NULL,
    transition_id BINARY(16) NOT NULL,
    status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    step VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_zone_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_endpoint VARCHAR(512) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_owner_epoch BIGINT UNSIGNED NOT NULL,
    source_route_version BIGINT UNSIGNED NOT NULL,
    source_lease_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    target_zone_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    target_endpoint VARCHAR(512) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    prepared_owner_epoch BIGINT UNSIGNED NOT NULL,
    prepared_route_version BIGINT UNSIGNED NOT NULL,
    prepared_lease_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    prepared_lease_term BIGINT UNSIGNED NOT NULL DEFAULT 1,
    players_json MEDIUMBLOB NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (logical_shard_id),
    KEY idx_shard_migration_progress_status (status),
    CONSTRAINT chk_shard_migration_progress_shard CHECK (logical_shard_id < 4096),
    CONSTRAINT chk_shard_migration_progress_status CHECK (status IN ('OPEN', 'ABANDONED')),
    CONSTRAINT chk_shard_migration_progress_step CHECK (
        step IN (
            'PREPARING_COMMITTED',
            'DRAINED',
            'FENCE_ADVANCED',
            'TARGET_PREPARED'
        )
    ),
    CONSTRAINT chk_shard_migration_progress_source_epoch CHECK (source_owner_epoch > 0),
    CONSTRAINT chk_shard_migration_progress_prepared_epoch CHECK (prepared_owner_epoch > 0)
) ENGINE = InnoDB;

INSERT IGNORE INTO schema_migrations (version) VALUES (5);
