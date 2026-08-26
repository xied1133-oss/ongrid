ALTER TABLE packet_captures
    ADD COLUMN network_namespace VARCHAR(128) NOT NULL DEFAULT '' AFTER interface_name;
