-- +goose Up
CREATE TABLE node_traffic_batch (
    node_id INT UNSIGNED NOT NULL,
    batch_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    payload_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (node_id, batch_id),
    KEY idx_traffic_batch_created (created_at)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE node_traffic_batch;
