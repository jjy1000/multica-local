-- 0.5.116 issue-deletion sync hardening (audit: 历史任务/实验室孤儿 + 裸 FK)。
-- Additive only — no table or column is dropped (forward-only law).
--
-- 1) The runtime GC deleted session rows but NO production path ever deleted
--    experimental_runtime_artifact rows — every GC'd session stranded its
--    artifacts in the DB forever. CASCADE ties artifact lifetime to session.
ALTER TABLE experimental_runtime_artifact
  ADD CONSTRAINT fk_experimental_runtime_artifact_session
  FOREIGN KEY (session_id) REFERENCES experimental_claude_runtime_session(id)
  ON DELETE CASCADE;

-- 2) issue-bound sessions/artifacts kept a dangling issue_id after the issue
--    was deleted: the by-issue endpoints and the 0.5.114 issue embed kept
--    "returning rows" for a ghost. SET NULL keeps the session history until
--    its regular 30-day GC expiry; the issue link is simply gone.
ALTER TABLE experimental_claude_runtime_session
  ADD CONSTRAINT fk_experimental_claude_runtime_session_issue
  FOREIGN KEY (issue_id) REFERENCES issue(id) ON DELETE SET NULL;

ALTER TABLE experimental_runtime_artifact
  ADD CONSTRAINT fk_experimental_runtime_artifact_issue
  FOREIGN KEY (issue_id) REFERENCES issue(id) ON DELETE SET NULL;
