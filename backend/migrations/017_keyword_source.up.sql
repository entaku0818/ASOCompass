-- Distinguish keywords added automatically by keyword discovery from those a
-- person added. Existing rows become 'manual'.
ALTER TABLE keywords
    ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT 'manual',
    ADD COLUMN IF NOT EXISTS auto_reason TEXT;
