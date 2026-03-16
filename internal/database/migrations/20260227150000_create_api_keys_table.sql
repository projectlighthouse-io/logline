-- +goose Up
CREATE TABLE api_keys (
    id          BIGSERIAL    PRIMARY KEY,
    name        TEXT         NOT NULL CHECK (length(name) <= 128),
    key_prefix  TEXT         NOT NULL CHECK (length(key_prefix) = 8),
    key_hash    TEXT         NOT NULL CHECK (length(key_hash) = 64),
    service     TEXT         NOT NULL CHECK (length(service) <= 128),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_api_keys_key_hash ON api_keys (key_hash);
CREATE INDEX idx_api_keys_service ON api_keys (service);

-- +goose Down
DROP TABLE api_keys;
