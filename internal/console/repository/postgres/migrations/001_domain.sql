CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (
        length(name) BETWEEN 1 AND 32
        AND name ~ '^[a-z](?:[a-z0-9-]{0,30}[a-z0-9])?$'
    ),
    state text NOT NULL CHECK (state IN ('active', 'inactive')),
    created_at timestamptz NOT NULL
);

CREATE TABLE camera_identities (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES sessions(id),
    name text NOT NULL CHECK (
        length(name) BETWEEN 1 AND 32
        AND name ~ '^[a-z](?:[a-z0-9-]{0,30}[a-z0-9])?$'
    ),
    created_at timestamptz NOT NULL,
    UNIQUE (id, session_id),
    UNIQUE (session_id, name)
);

CREATE TABLE camera_connections (
    camera_identity_id uuid PRIMARY KEY REFERENCES camera_identities(id),
    url text,
    status text NOT NULL CHECK (status IN ('activating', 'waiting', 'connected', 'error')),
    error text,
    video_worker_id uuid,
    CHECK (status = 'activating' OR url IS NOT NULL),
    CHECK ((status = 'error') = (error IS NOT NULL AND length(error) > 0))
);

CREATE TABLE takes (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES sessions(id),
    name text NOT NULL CHECK (
        length(name) BETWEEN 1 AND 32
        AND name ~ '^[a-z](?:[a-z0-9-]{0,30}[a-z0-9])?$'
    ),
    phase text NOT NULL CHECK (phase IN ('ongoing', 'finished')),
    state text CHECK (state IN ('uploading', 'completed', 'errored')),
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    error text,
    UNIQUE (id, session_id),
    UNIQUE (session_id, name),
    CHECK (
        (phase = 'ongoing' AND state IS NULL AND finished_at IS NULL AND error IS NULL)
        OR
        (phase = 'finished' AND state IS NOT NULL AND finished_at IS NOT NULL AND finished_at >= started_at)
    ),
    CHECK ((state = 'errored') = (error IS NOT NULL AND length(error) > 0))
);

CREATE UNIQUE INDEX one_ongoing_take_per_session
    ON takes (session_id)
    WHERE phase = 'ongoing';

CREATE TABLE recording_cameras (
    take_id uuid NOT NULL,
    camera_identity_id uuid NOT NULL,
    session_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('recording', 'errored')),
    started_at timestamptz NOT NULL,
    error text,
    PRIMARY KEY (take_id, camera_identity_id),
    FOREIGN KEY (take_id, session_id) REFERENCES takes(id, session_id),
    FOREIGN KEY (camera_identity_id, session_id) REFERENCES camera_identities(id, session_id),
    FOREIGN KEY (camera_identity_id) REFERENCES camera_connections(camera_identity_id),
    CHECK ((state = 'errored') = (error IS NOT NULL AND length(error) > 0))
);

CREATE TABLE video_files (
    take_id uuid NOT NULL,
    camera_identity_id uuid NOT NULL,
    session_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('uploading', 'completed', 'errored')),
    started_at timestamptz NOT NULL,
    finished_at timestamptz NOT NULL CHECK (finished_at >= started_at),
    object_key text,
    hash bytea CHECK (hash IS NULL OR octet_length(hash) = 32),
    size bigint CHECK (size IS NULL OR size >= 0),
    error text,
    PRIMARY KEY (take_id, camera_identity_id),
    FOREIGN KEY (take_id, session_id) REFERENCES takes(id, session_id),
    FOREIGN KEY (camera_identity_id, session_id) REFERENCES camera_identities(id, session_id),
    CHECK (
        state <> 'completed'
        OR (object_key IS NOT NULL AND length(object_key) > 0 AND hash IS NOT NULL AND size IS NOT NULL)
    ),
    CHECK ((state = 'errored') = (error IS NOT NULL AND length(error) > 0))
);
