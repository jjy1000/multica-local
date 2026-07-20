-- 163_lab_source_code_canvas (0.3.54)
-- ----------------------------------------------------------------------------
-- Add `code_canvas` to experimental_resource_lock.experimental_source CHECK.
-- 0.3.51 staged the lab's run.sh stub subprocess but never wired an install
-- handler / agent row, so the CHECK omitted the value. 0.3.54 ships
-- install_code_canvas.go which claims a code_canvas_worker leader agent;
-- the CHECK needs the value or Claim(HideAgent) fails on insert with a
-- constraint violation.
--
-- Forward-only: drop + re-add the constraint with `code_canvas` appended.
-- Same shape as mig 160; the existing rows are unaffected (only inserts
-- are checked).
-- ----------------------------------------------------------------------------

ALTER TABLE experimental_resource_lock
    DROP CONSTRAINT experimental_resource_lock_experimental_source_check;

ALTER TABLE experimental_resource_lock
    ADD CONSTRAINT experimental_resource_lock_experimental_source_check
    CHECK (experimental_source = ANY (ARRAY[
        'claude_science'::text,
        'claude_science_lab'::text,
        'mythos_swarm'::text,
        'agent_self_optimization'::text,
        'pythia_oracle'::text,
        'llm_wiki_bridge'::text,
        'constitution_agent'::text,
        'code_canvas'::text
    ]));
