ALTER TABLE keywords
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS auto_reason;
