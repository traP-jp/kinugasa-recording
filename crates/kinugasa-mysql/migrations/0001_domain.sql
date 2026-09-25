CREATE TABLE sessions (
    id BINARY(16) PRIMARY KEY,
    name VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
    state ENUM('active', 'inactive') NOT NULL,
    created_at DATETIME(6) NOT NULL,
    CONSTRAINT sessions_name_valid CHECK (
        CHAR_LENGTH(name) BETWEEN 1 AND 32
        AND name REGEXP '^[a-z]([a-z0-9-]{0,30}[a-z0-9])?$'
    )
) ENGINE = InnoDB;

CREATE TABLE camera_identities (
    id BINARY(16) PRIMARY KEY,
    session_id BINARY(16) NOT NULL,
    name VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    created_at DATETIME(6) NOT NULL,
    CONSTRAINT camera_identities_session_name_unique UNIQUE (session_id, name),
    CONSTRAINT camera_identities_id_session_unique UNIQUE (id, session_id),
    CONSTRAINT camera_identities_session_fk FOREIGN KEY (session_id) REFERENCES sessions(id),
    CONSTRAINT camera_identities_name_valid CHECK (
        CHAR_LENGTH(name) BETWEEN 1 AND 32
        AND name REGEXP '^[a-z]([a-z0-9-]{0,30}[a-z0-9])?$'
    )
) ENGINE = InnoDB;

CREATE TABLE camera_connections (
    camera_identity_id BINARY(16) PRIMARY KEY,
    url TEXT,
    status ENUM('activating', 'waiting', 'connected', 'error') NOT NULL,
    error TEXT,
    media_process_id BINARY(16),
    deletion_requested_at DATETIME(6),
    CONSTRAINT camera_connections_identity_fk
        FOREIGN KEY (camera_identity_id) REFERENCES camera_identities(id),
    CONSTRAINT camera_connections_url_valid CHECK (
        status = 'activating' OR url IS NOT NULL
    ),
    CONSTRAINT camera_connections_error_valid CHECK (
        (status = 'error') = (error IS NOT NULL AND CHAR_LENGTH(error) > 0)
    ),
    INDEX camera_connections_deletion_requested_idx (deletion_requested_at)
) ENGINE = InnoDB;

CREATE TABLE takes (
    id BINARY(16) PRIMARY KEY,
    session_id BINARY(16) NOT NULL,
    name VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    phase ENUM('ongoing', 'finished') NOT NULL,
    state ENUM('uploading', 'completed', 'errored'),
    started_at DATETIME(6) NOT NULL,
    finished_at DATETIME(6),
    error TEXT,
    ongoing_slot TINYINT GENERATED ALWAYS AS (
        CASE WHEN phase = 'ongoing' THEN 1 ELSE NULL END
    ) STORED,
    CONSTRAINT takes_session_name_unique UNIQUE (session_id, name),
    CONSTRAINT takes_id_session_unique UNIQUE (id, session_id),
    CONSTRAINT takes_one_ongoing_unique UNIQUE (session_id, ongoing_slot),
    CONSTRAINT takes_session_fk FOREIGN KEY (session_id) REFERENCES sessions(id),
    CONSTRAINT takes_name_valid CHECK (
        CHAR_LENGTH(name) BETWEEN 1 AND 32
        AND name REGEXP '^[a-z]([a-z0-9-]{0,30}[a-z0-9])?$'
    ),
    CONSTRAINT takes_phase_valid CHECK (
        (phase = 'ongoing' AND state IS NULL AND finished_at IS NULL AND error IS NULL)
        OR
        (phase = 'finished' AND state IS NOT NULL AND finished_at IS NOT NULL
            AND finished_at >= started_at)
    ),
    CONSTRAINT takes_error_valid CHECK (
        (state = 'errored') = (error IS NOT NULL AND CHAR_LENGTH(error) > 0)
    )
) ENGINE = InnoDB;

CREATE TABLE recording_cameras (
    take_id BINARY(16) NOT NULL,
    camera_identity_id BINARY(16) NOT NULL,
    session_id BINARY(16) NOT NULL,
    state ENUM('recording', 'errored') NOT NULL,
    started_at DATETIME(6) NOT NULL,
    error TEXT,
    PRIMARY KEY (take_id, camera_identity_id),
    CONSTRAINT recording_cameras_take_fk
        FOREIGN KEY (take_id, session_id) REFERENCES takes(id, session_id),
    CONSTRAINT recording_cameras_identity_session_fk
        FOREIGN KEY (camera_identity_id, session_id)
        REFERENCES camera_identities(id, session_id),
    CONSTRAINT recording_cameras_connection_fk
        FOREIGN KEY (camera_identity_id) REFERENCES camera_connections(camera_identity_id),
    CONSTRAINT recording_cameras_error_valid CHECK (
        (state = 'errored') = (error IS NOT NULL AND CHAR_LENGTH(error) > 0)
    ),
    INDEX recording_cameras_camera_state_idx (camera_identity_id, state)
) ENGINE = InnoDB;

CREATE TABLE video_files (
    take_id BINARY(16) NOT NULL,
    camera_identity_id BINARY(16) NOT NULL,
    session_id BINARY(16) NOT NULL,
    state ENUM('uploading', 'completed', 'errored') NOT NULL,
    started_at DATETIME(6) NOT NULL,
    finished_at DATETIME(6) NOT NULL,
    object_key TEXT,
    hash BINARY(32),
    size BIGINT UNSIGNED,
    error TEXT,
    PRIMARY KEY (take_id, camera_identity_id),
    CONSTRAINT video_files_take_fk
        FOREIGN KEY (take_id, session_id) REFERENCES takes(id, session_id),
    CONSTRAINT video_files_identity_session_fk
        FOREIGN KEY (camera_identity_id, session_id)
        REFERENCES camera_identities(id, session_id),
    CONSTRAINT video_files_time_valid CHECK (finished_at >= started_at),
    CONSTRAINT video_files_completed_valid CHECK (
        state <> 'completed'
        OR (object_key IS NOT NULL AND CHAR_LENGTH(object_key) > 0
            AND hash IS NOT NULL AND size IS NOT NULL)
    ),
    CONSTRAINT video_files_error_valid CHECK (
        (state = 'errored') = (error IS NOT NULL AND CHAR_LENGTH(error) > 0)
    ),
    INDEX video_files_session_state_idx (session_id, state),
    INDEX video_files_camera_state_idx (camera_identity_id, state)
) ENGINE = InnoDB;

CREATE TABLE finalized_recordings (
    take_id BINARY(16) NOT NULL,
    camera_identity_id BINARY(16) NOT NULL,
    session_id BINARY(16) NOT NULL,
    started_at DATETIME(6) NOT NULL,
    finished_at DATETIME(6) NOT NULL,
    relative_path TEXT NOT NULL,
    media_type VARCHAR(255) NOT NULL,
    PRIMARY KEY (take_id, camera_identity_id),
    CONSTRAINT finalized_recordings_take_fk
        FOREIGN KEY (take_id, session_id) REFERENCES takes(id, session_id),
    CONSTRAINT finalized_recordings_identity_session_fk
        FOREIGN KEY (camera_identity_id, session_id)
        REFERENCES camera_identities(id, session_id),
    CONSTRAINT finalized_recordings_time_valid CHECK (finished_at >= started_at),
    CONSTRAINT finalized_recordings_path_valid CHECK (CHAR_LENGTH(relative_path) > 0),
    CONSTRAINT finalized_recordings_media_type_valid CHECK (CHAR_LENGTH(media_type) > 0),
    INDEX finalized_recordings_session_idx (session_id)
) ENGINE = InnoDB;
