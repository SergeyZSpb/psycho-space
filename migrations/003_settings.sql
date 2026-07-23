-- 003_settings: key/value app settings. open_registration, when 'true',
-- auto-approves newly created accounts on first login (superadmin toggle).

CREATE TABLE app_settings (
    key        text PRIMARY KEY,
    value      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO app_settings (key, value) VALUES ('open_registration', 'false');
