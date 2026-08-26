UPDATE alert_silences
SET name = LEFT(name, 128)
WHERE CHAR_LENGTH(name) > 128;

ALTER TABLE alert_silences
    MODIFY COLUMN name VARCHAR(128) NOT NULL DEFAULT '';
