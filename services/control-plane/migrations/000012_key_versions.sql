-- AgentOS 000012 — key-version columns for secret rotation.
-- Ciphertext envelopes are versioned so multiple master keys can coexist
-- during rotation; the version travels with each stored secret.

ALTER TABLE model_credentials ADD COLUMN key_version smallint NOT NULL DEFAULT 1;
ALTER TABLE webhooks ADD COLUMN key_version smallint NOT NULL DEFAULT 1;
