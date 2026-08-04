CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT UNSIGNED NOT NULL,
    applied_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (version)
) ENGINE = InnoDB;

INSERT IGNORE INTO schema_migrations (version) VALUES (1);
