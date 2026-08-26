ALTER TABLE packet_captures
    ADD COLUMN live_preview_json TEXT NOT NULL AFTER captured_packets;
