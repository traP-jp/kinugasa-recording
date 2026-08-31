ALTER TABLE camera_connections
    ADD COLUMN deletion_requested_at timestamptz;

CREATE INDEX camera_connections_deletion_requested_idx
    ON camera_connections (deletion_requested_at)
    WHERE deletion_requested_at IS NOT NULL;
