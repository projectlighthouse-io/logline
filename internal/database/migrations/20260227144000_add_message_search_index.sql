-- +goose Up
CREATE INDEX idx_logs_message_search ON logs USING GIN (to_tsvector('english', message));

-- +goose Down
DROP INDEX idx_logs_message_search;
