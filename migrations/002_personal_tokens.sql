DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'clients' AND column_name = 'key_hash'
    ) THEN
        ALTER TABLE clients RENAME COLUMN key_hash TO token_hash;
    END IF;
END $$;

ALTER TABLE clients ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE clients DROP CONSTRAINT IF EXISTS clients_role_check;
UPDATE clients SET role = 'reporter' WHERE role = 'correspondent';
ALTER TABLE clients ADD CONSTRAINT clients_role_check CHECK (role IN ('reporter', 'editor'));
CREATE UNIQUE INDEX IF NOT EXISTS clients_token_hash ON clients (token_hash);
