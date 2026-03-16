-- +goose Up
CREATE TABLE users (
    id            BIGSERIAL    PRIMARY KEY,
    email         TEXT         NOT NULL UNIQUE CHECK (length(email) <= 254),
    password_hash TEXT         NOT NULL CHECK (length(password_hash) = 60),
    name          TEXT         NOT NULL CHECK (length(name) <= 128),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE users;
