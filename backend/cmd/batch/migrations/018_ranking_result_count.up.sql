-- How many results the store search returned when the rank was recorded, so a
-- null rank ("not in the results") can be told apart from a search that came
-- back unusually short. Existing rows stay NULL (count unknown).
ALTER TABLE ranking_history
    ADD COLUMN IF NOT EXISTS result_count INTEGER;
