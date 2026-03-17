-- +goose Up
CREATE INDEX idx_logs_service ON logs (service);

-- +goose Down
DROP INDEX idx_logs_service;
