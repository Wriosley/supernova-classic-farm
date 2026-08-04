CREATE TABLE IF NOT EXISTS shard_fences (
    logical_shard_id INT UNSIGNED NOT NULL,
    owner_zone_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    owner_epoch BIGINT UNSIGNED NOT NULL,
    route_version BIGINT UNSIGNED NOT NULL,
    transition_id BINARY(16) NOT NULL,
    fenced_at_ms BIGINT NOT NULL,
    PRIMARY KEY (logical_shard_id),
    CONSTRAINT chk_shard_fences_logical_shard CHECK (logical_shard_id < 4096),
    CONSTRAINT chk_shard_fences_owner_epoch CHECK (owner_epoch > 0)
) ENGINE = InnoDB;

INSERT IGNORE INTO shard_fences (
    logical_shard_id,
    owner_zone_id,
    owner_epoch,
    route_version,
    transition_id,
    fenced_at_ms
)
SELECT
    shard_ids.shard_id,
    'zone-local',
    1,
    1,
    UNHEX(MD5(CONCAT('classic-farm-local-fence-', shard_ids.shard_id))),
    UNIX_TIMESTAMP(CURRENT_TIMESTAMP(3)) * 1000
FROM (
    SELECT ones.n + tens.n * 10 + hundreds.n * 100 + thousands.n * 1000 AS shard_id
    FROM
        (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4
         UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) ones
    CROSS JOIN
        (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4
         UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) tens
    CROSS JOIN
        (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4
         UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) hundreds
    CROSS JOIN
        (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4
         UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) thousands
) AS shard_ids
WHERE shard_ids.shard_id < 4096;

INSERT IGNORE INTO schema_migrations (version) VALUES (3);
