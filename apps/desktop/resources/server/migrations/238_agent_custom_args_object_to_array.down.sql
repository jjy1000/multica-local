-- 0.5.12 down: no-op. The up migration repaired invalid '{}' objects to
-- '[]' arrays; the original malformed values were never valid data (the
-- column contract is a JSON array of CLI argument strings, migration 041)
-- and restoring them would only re-introduce the unmarshal WARN spam.
SELECT 1;
