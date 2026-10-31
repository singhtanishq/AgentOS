-- AgentOS 000001 — extensions and shared helpers.
-- Executed as the migration owner; the application role receives grants in 000009.

-- Case-insensitive text for emails and slugs.
CREATE EXTENSION IF NOT EXISTS citext;

-- Shared updated_at maintenance trigger.
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Normalizes a role/action/status-like text value: lowercase, trimmed.
CREATE OR REPLACE FUNCTION normalize_enum(v text) RETURNS text AS $$
BEGIN
    RETURN lower(btrim(v));
END;
$$ LANGUAGE plpgsql IMMUTABLE;
