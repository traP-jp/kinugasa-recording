ALTER TABLE camera_connections
    ADD COLUMN virtual_port SMALLINT UNSIGNED NULL AFTER url;

UPDATE camera_connections
SET virtual_port = CAST(
    SUBSTRING_INDEX(SUBSTRING_INDEX(url, 'virt-dst-port=', -1), '&', 1)
    AS UNSIGNED
)
WHERE url REGEXP '(^|[?&])virt-dst-port=[0-9]+(&|$)';

ALTER TABLE camera_connections
    ADD CONSTRAINT camera_connections_virtual_port_valid CHECK (
        virtual_port IS NULL OR virtual_port BETWEEN 1024 AND 65534
    );
