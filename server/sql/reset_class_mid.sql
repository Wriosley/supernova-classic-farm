SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;
DROP TABLE IF EXISTS class_mid_friends;
DROP TABLE IF EXISTS class_mid_mails;
DROP TABLE IF EXISTS class_mid_player_tasks;
DROP TABLE IF EXISTS class_mid_tasks;
DROP TABLE IF EXISTS class_mid_plots;
DROP TABLE IF EXISTS class_mid_inventory;
DROP TABLE IF EXISTS class_mid_crops;
DROP TABLE IF EXISTS class_mid_players;
DROP TABLE IF EXISTS class_mid_states;
DROP TABLE IF EXISTS class_mid_accounts;
SET FOREIGN_KEY_CHECKS = 1;

CREATE TABLE class_mid_accounts (
    player_id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    username VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
    password VARCHAR(64) NOT NULL
) ENGINE=InnoDB;

CREATE TABLE class_mid_players (
    player_id BIGINT UNSIGNED PRIMARY KEY,
    coins INT NOT NULL DEFAULT 50,
    fertilizer INT NOT NULL DEFAULT 2,
    chapter INT NOT NULL DEFAULT 1,
    state_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    FOREIGN KEY (player_id) REFERENCES class_mid_accounts(player_id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE class_mid_crops (
    crop_id INT PRIMARY KEY,
    crop_name VARCHAR(30) NOT NULL,
    seed_price INT NOT NULL,
    sale_price INT NOT NULL,
    mature_seconds INT NOT NULL,
    yield_count INT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE class_mid_inventory (
    player_id BIGINT UNSIGNED NOT NULL,
    crop_id INT NOT NULL,
    seed_count INT NOT NULL DEFAULT 0,
    crop_count INT NOT NULL DEFAULT 0,
    PRIMARY KEY (player_id, crop_id),
    FOREIGN KEY (player_id) REFERENCES class_mid_players(player_id) ON DELETE CASCADE,
    FOREIGN KEY (crop_id) REFERENCES class_mid_crops(crop_id)
) ENGINE=InnoDB;

CREATE TABLE class_mid_plots (
    player_id BIGINT UNSIGNED NOT NULL,
    plot_no INT NOT NULL,
    crop_id INT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'EMPTY',
    planted_at_ms BIGINT NOT NULL DEFAULT 0,
    mature_at_ms BIGINT NOT NULL DEFAULT 0,
    fertilized BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (player_id, plot_no),
    FOREIGN KEY (player_id) REFERENCES class_mid_players(player_id) ON DELETE CASCADE,
    FOREIGN KEY (crop_id) REFERENCES class_mid_crops(crop_id)
) ENGINE=InnoDB;

CREATE TABLE class_mid_tasks (
    task_id INT PRIMARY KEY,
    action VARCHAR(40) NOT NULL UNIQUE,
    label VARCHAR(100) NOT NULL,
    target_count INT NOT NULL,
    reward_coins INT NOT NULL DEFAULT 0
) ENGINE=InnoDB;

CREATE TABLE class_mid_player_tasks (
    player_id BIGINT UNSIGNED NOT NULL,
    task_id INT NOT NULL,
    current_count INT NOT NULL DEFAULT 0,
    is_claimed BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (player_id, task_id),
    FOREIGN KEY (player_id) REFERENCES class_mid_players(player_id) ON DELETE CASCADE,
    FOREIGN KEY (task_id) REFERENCES class_mid_tasks(task_id)
) ENGINE=InnoDB;

CREATE TABLE class_mid_mails (
    mail_id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    sender_id BIGINT UNSIGNED NULL,
    receiver_id BIGINT UNSIGNED NOT NULL,
    title VARCHAR(100) NOT NULL,
    content TEXT NOT NULL,
    is_read BOOLEAN NOT NULL DEFAULT FALSE,
    created_at_ms BIGINT NOT NULL,
    INDEX idx_class_mid_mails_receiver (receiver_id, mail_id),
    FOREIGN KEY (sender_id) REFERENCES class_mid_players(player_id) ON DELETE SET NULL,
    FOREIGN KEY (receiver_id) REFERENCES class_mid_players(player_id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE class_mid_friends (
    player_id BIGINT UNSIGNED NOT NULL,
    friend_id BIGINT UNSIGNED NOT NULL,
    created_at_ms BIGINT NOT NULL,
    PRIMARY KEY (player_id, friend_id),
    FOREIGN KEY (player_id) REFERENCES class_mid_players(player_id) ON DELETE CASCADE,
    FOREIGN KEY (friend_id) REFERENCES class_mid_players(player_id) ON DELETE CASCADE
) ENGINE=InnoDB;

INSERT INTO class_mid_crops VALUES
    (1, '胡萝卜', 2, 5, 30, 3),
    (2, '玉米', 3, 7, 35, 3),
    (3, '土豆', 4, 9, 40, 4),
    (4, '番茄', 5, 11, 45, 4),
    (5, '草莓', 6, 14, 50, 5),
    (6, '南瓜', 8, 18, 60, 5);

INSERT INTO class_mid_tasks VALUES
    (1, 'BUY_SEEDS', '购买3颗种子', 3, 0),
    (2, 'PLANT', '种植1次', 1, 0),
    (3, 'APPLY_FERTILIZER', '施肥1次', 1, 0),
    (4, 'HARVEST', '收获1次', 1, 0),
    (5, 'SELL_CROP', '出售1份作物', 1, 10);
