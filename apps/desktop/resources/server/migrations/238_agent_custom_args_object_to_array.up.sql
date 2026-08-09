-- 0.5.12: repair agent rows whose custom_args holds a JSON object ('{}')
-- instead of an array. The lab install handlers (install_mythos /
-- install_pythia / install_claude_science / install_code_canvas) wrote
-- []byte("{}") until this release, but every reader unmarshals the column
-- into []string, so each read of an affected row logs
-- "failed to unmarshal agent custom_args" (10k+ WARN lines observed).
-- The fix pairs with switching those writers to '[]'. Idempotent: only
-- touches rows whose JSON type is not already 'array'.
UPDATE agent
SET custom_args = '[]'::jsonb
WHERE custom_args IS NOT NULL
  AND jsonb_typeof(custom_args) <> 'array';
