CREATE TABLE worker_processes (
    worker_id uuid PRIMARY KEY,
    session_id uuid NOT NULL,
    camera_identity_id uuid NOT NULL,
    first_registered_at timestamptz NOT NULL,
    last_registered_at timestamptz NOT NULL,
    last_snapshot_sequence bigint NOT NULL CHECK (last_snapshot_sequence >= 0),
    last_event_sequence bigint NOT NULL CHECK (last_event_sequence >= 0),
    snapshot bytea NOT NULL,
    FOREIGN KEY (camera_identity_id, session_id) REFERENCES camera_identities(id, session_id)
);

CREATE TABLE worker_events (
    event_id uuid PRIMARY KEY,
    worker_id uuid NOT NULL REFERENCES worker_processes(worker_id),
    sequence bigint NOT NULL CHECK (sequence > 0),
    occurred_at timestamptz NOT NULL,
    payload bytea NOT NULL,
    UNIQUE (worker_id, sequence)
);

CREATE TABLE worker_commands (
    command_id uuid PRIMARY KEY,
    camera_identity_id uuid NOT NULL REFERENCES camera_identities(id),
    take_id uuid REFERENCES takes(id),
    issued_at timestamptz NOT NULL,
    payload bytea NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (
        status IN ('pending', 'applied', 'already_applied', 'rejected', 'failed')
    ),
    completed_at timestamptz,
    result bytea,
    CHECK (
        (status = 'pending' AND completed_at IS NULL AND result IS NULL)
        OR
        (status <> 'pending' AND completed_at IS NOT NULL AND result IS NOT NULL)
    )
);

CREATE TABLE finalized_recordings (
    take_id uuid NOT NULL,
    camera_identity_id uuid NOT NULL,
    session_id uuid NOT NULL,
    started_at timestamptz NOT NULL,
    finished_at timestamptz NOT NULL CHECK (finished_at >= started_at),
    relative_path text NOT NULL CHECK (length(relative_path) > 0),
    media_type text NOT NULL CHECK (media_type = 'video/mp4'),
    PRIMARY KEY (take_id, camera_identity_id),
    FOREIGN KEY (take_id, session_id) REFERENCES takes(id, session_id),
    FOREIGN KEY (camera_identity_id, session_id) REFERENCES camera_identities(id, session_id)
);
