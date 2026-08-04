CREATE TABLE IF NOT EXISTS accounts (
    player_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    account_name VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    password_salt VARBINARY(64) NOT NULL,
    password_digest VARBINARY(64) NOT NULL,
    password_memory_kib INT UNSIGNED NOT NULL,
    password_iterations INT UNSIGNED NOT NULL,
    password_threads INT UNSIGNED NOT NULL,
    password_version INT UNSIGNED NOT NULL,
    session_generation BIGINT UNSIGNED NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (player_id),
    UNIQUE KEY uq_accounts_account_name (account_name),
    CONSTRAINT chk_accounts_status CHECK (status IN ('ACTIVE', 'DISABLED')),
    CONSTRAINT chk_accounts_session_generation CHECK (session_generation > 0)
) ENGINE = InnoDB;

CREATE TABLE IF NOT EXISTS player_checkpoints (
    player_id BIGINT UNSIGNED NOT NULL,
    db_shard_id INT UNSIGNED NOT NULL,
    logical_shard_id INT UNSIGNED NOT NULL,
    owner_epoch BIGINT UNSIGNED NOT NULL,
    player_seq BIGINT UNSIGNED NOT NULL,
    checkpoint_revision BIGINT UNSIGNED NOT NULL,
    checkpoint_schema_version INT UNSIGNED NOT NULL,
    checkpoint_blob MEDIUMBLOB NOT NULL,
    checkpoint_sha256 BINARY(32) NOT NULL,
    last_applied_config_version BIGINT UNSIGNED NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (player_id),
    KEY idx_player_checkpoints_logical_shard (logical_shard_id),
    KEY idx_player_checkpoints_updated_at (updated_at_ms),
    CONSTRAINT fk_player_checkpoints_account
        FOREIGN KEY (player_id) REFERENCES accounts (player_id),
    CONSTRAINT chk_player_checkpoints_logical_shard CHECK (logical_shard_id < 4096),
    CONSTRAINT chk_player_checkpoints_schema CHECK (checkpoint_schema_version > 0),
    CONSTRAINT chk_player_checkpoints_blob_size CHECK (OCTET_LENGTH(checkpoint_blob) <= 4194304)
) ENGINE = InnoDB;

CREATE TABLE IF NOT EXISTS auth_sessions (
    session_digest BINARY(32) NOT NULL,
    player_id BIGINT UNSIGNED NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    created_at_ms BIGINT NOT NULL,
    idle_expires_at_ms BIGINT NOT NULL,
    absolute_expires_at_ms BIGINT NOT NULL,
    revoked BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (session_digest),
    KEY idx_auth_sessions_player_generation (player_id, generation),
    KEY idx_auth_sessions_expiry (revoked, absolute_expires_at_ms),
    CONSTRAINT fk_auth_sessions_account
        FOREIGN KEY (player_id) REFERENCES accounts (player_id)
) ENGINE = InnoDB;

INSERT IGNORE INTO schema_migrations (version) VALUES (2);
