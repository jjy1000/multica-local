// Package agent_self_optimization — edits.go (0.5.2, generalized 0.5.3).
//
// Human-confirm + rollback actions on the agent_opt_edit ledger. These are
// the "待确认建议" tier's write paths:
//
//	ApplyEdit   — a suggested (or rejected) edit is applied by the user.
//	              The server RE-CHECKS the design-review gates (§5) at apply
//	              time (client eligibility is never trusted), snapshots the
//	              pre-edit instruction set, writes the edit to the subject's
//	              instruction text, and marks the row applied_by=user.
//	RejectEdit  — explicit user reject (soft evidence, undo-able).
//	IgnoreEdit  — soft archive (NOT in the rejection buffer; re-proposable).
//	RevertEdit  — roll back an applied edit to its snapshot (the
//	              "回退到上一版本" action).
//
// 0.5.3 generalization: the ledger is polymorphic — target_type ∈
// (agent | skill | squad | autopilot). Apply/Revert resolve the subject
// via its workspace-scoped getter and write back through the
// content-only updater (never clobbering fields the user changed
// meanwhile). The workspace is resolved at the boundary so a stale
// client id cannot mis-target a different workspace's edit.
package agent_self_optimization

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// loadSubjectText returns the subject's CURRENT trainable instruction
// text, resolved via its workspace-scoped getter (tenant guard).
func (s *Service) loadSubjectText(ctx context.Context, workspaceID pgtype.UUID, targetType string, targetID pgtype.UUID) (string, error) {
	switch targetType {
	case SubjectAgent:
		agent, err := s.queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          targetID,
			WorkspaceID: workspaceID,
		})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(agent.Instructions), nil
	case SubjectSkill:
		sk, err := s.queries.GetSkillInWorkspace(ctx, db.GetSkillInWorkspaceParams{
			ID:          targetID,
			WorkspaceID: workspaceID,
		})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(sk.Content), nil
	case SubjectSquad:
		sq, err := s.queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
			ID:          targetID,
			WorkspaceID: workspaceID,
		})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(sq.Instructions), nil
	case SubjectAutopilot:
		ap, err := s.queries.GetAutopilotInWorkspace(ctx, db.GetAutopilotInWorkspaceParams{
			ID:          targetID,
			WorkspaceID: workspaceID,
		})
		if err != nil {
			return "", err
		}
		// The trainable text of an autopilot is its issue title template
		// (what it generates); description is a supporting brief.
		if ap.IssueTitleTemplate.Valid && strings.TrimSpace(ap.IssueTitleTemplate.String) != "" {
			return strings.TrimSpace(ap.IssueTitleTemplate.String), nil
		}
		return strings.TrimSpace(ap.Description.String), nil
	default:
		return "", fmt.Errorf("apply edit: unknown target_type %q", targetType)
	}
}

// writeBackSubjectText persists the new instruction text onto the subject
// row via the content-only updaters (never clobbering user-edited fields).
// Autopilots write the issue_title_template (the trainable text).
func (s *Service) writeBackSubjectText(ctx context.Context, workspaceID pgtype.UUID, targetType string, targetID pgtype.UUID, text string) error {
	switch targetType {
	case SubjectAgent:
		_, err := s.queries.UpdateAgent(ctx, db.UpdateAgentParams{
			ID:           targetID,
			Instructions: pgtype.Text{String: text, Valid: true},
		})
		return err
	case SubjectSkill:
		_, err := s.queries.UpdateSkillContent(ctx, db.UpdateSkillContentParams{
			ID:          targetID,
			Content:     text,
			WorkspaceID: workspaceID,
		})
		return err
	case SubjectSquad:
		_, err := s.queries.UpdateSquadInstructions(ctx, db.UpdateSquadInstructionsParams{
			ID:           targetID,
			Instructions: text,
			WorkspaceID:  workspaceID,
		})
		return err
	case SubjectAutopilot:
		_, err := s.queries.UpdateAutopilotPrompt(ctx, db.UpdateAutopilotPromptParams{
			ID:                 targetID,
			Description:        pgtype.Text{}, // untouched
			IssueTitleTemplate: pgtype.Text{String: text, Valid: true},
			WorkspaceID:        workspaceID,
		})
		return err
	default:
		return fmt.Errorf("write back: unknown target_type %q", targetType)
	}
}

// ApplyEdit applies a stored edit to the subject's current instruction
// text.
//
// Gates re-checked server-side (design-review §4 — client eligibility is
// never trusted):
//   - the edit must exist in the workspace
//   - the edit must be 'suggested' or 'rejected' (an already-applied or
//     reverted edit cannot be applied again; an ignored edit may be)
//   - the edit's 'before' text must still be present in the current
//     instruction text for delete/replace (graceful "no longer applicable"
//     when the subject's text changed since the edit was proposed)
//
// It snapshots the pre-edit set, updates the subject's instruction text,
// and marks the row applied (applied_by=user). Returns the edit id.
func (s *Service) ApplyEdit(ctx context.Context, workspaceID, editID pgtype.UUID) (pgtype.UUID, error) {
	edit, err := s.queries.GetAgentOptEdit(ctx, db.GetAgentOptEditParams{
		ID:          editID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("apply edit: %w", err)
	}
	switch edit.Application {
	case string(ApplicationApplied):
		return edit.ID, nil // idempotent
	case string(ApplicationReverted):
		return pgtype.UUID{}, fmt.Errorf("edit was reverted; re-apply not supported")
	}

	targetType := edit.TargetType
	if targetType == "" {
		// Legacy row (pre-0.5.3): agent subject.
		targetType = SubjectAgent
	}
	cur, err := s.loadSubjectText(ctx, workspaceID, targetType, edit.TargetID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("apply edit: subject lookup: %w", err)
	}
	// Snapshot = the PRE-EDIT set (the rollback point). Captured BEFORE
	// the switch mutates cur.
	preEdit := cur

	// Re-apply the edit onto the CURRENT instruction text.
	switch edit.EditType {
	case "add":
		if cur != "" && !strings.HasSuffix(cur, "\n") {
			cur += "\n"
		}
		cur += edit.AfterText + "\n"
	case "delete":
		if edit.BeforeText == "" || !strings.Contains(cur, edit.BeforeText) {
			return pgtype.UUID{}, fmt.Errorf("no longer applicable: instruction text changed")
		}
		cur = strings.TrimSpace(strings.Replace(cur, edit.BeforeText, "", 1))
	case "replace":
		if edit.BeforeText == "" || !strings.Contains(cur, edit.BeforeText) {
			return pgtype.UUID{}, fmt.Errorf("no longer applicable: instruction text changed")
		}
		cur = strings.Replace(cur, edit.BeforeText, edit.AfterText, 1)
	default:
		return pgtype.UUID{}, fmt.Errorf("unknown edit_type %q", edit.EditType)
	}

	// Order inversion guards the double-apply race (correctness finding
	// c2): mark the row applied FIRST (snapshot = the PRE-EDIT set) so a
	// concurrent second click sees application='applied' and hits the
	// idempotent branch above instead of double-appending. The instruction
	// write follows; if it fails the row is already applied and the user
	// can revert via the snapshot rather than re-applying.
	if _, uerr := s.queries.UpdateAgentOptEditApplication(ctx, db.UpdateAgentOptEditApplicationParams{
		ID:                   editID,
		Application:          string(ApplicationApplied),
		WorkspaceID:          workspaceID,
		InstructionsSnapshot: pgtype.Text{String: preEdit, Valid: true},
		AppliedBy:            pgtype.Text{String: "user", Valid: true},
	}); uerr != nil {
		return pgtype.UUID{}, fmt.Errorf("apply edit: mark applied: %w", uerr)
	}

	if err := s.writeBackSubjectText(ctx, workspaceID, targetType, edit.TargetID, cur); err != nil {
		return pgtype.UUID{}, fmt.Errorf("apply edit: write instruction text: %w", err)
	}
	return editID, nil
}

// RejectEdit soft-rejects a suggestion. Rejected edits are negative
// evidence (never re-proposed identically) but remain undo-able and
// re-proposable with materially stronger backing.
func (s *Service) RejectEdit(ctx context.Context, workspaceID, editID pgtype.UUID, reason string) error {
	edit, err := s.queries.GetAgentOptEdit(ctx, db.GetAgentOptEditParams{ID: editID, WorkspaceID: workspaceID})
	if err != nil {
		return fmt.Errorf("reject edit: %w", err)
	}
	if edit.Application == string(ApplicationRejected) {
		return nil // idempotent
	}
	_, err = s.queries.UpdateAgentOptEditApplication(ctx, db.UpdateAgentOptEditApplicationParams{
		ID:          editID,
		Application: string(ApplicationRejected),
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return fmt.Errorf("reject edit: %w", err)
	}
	return nil
}

// IgnoreEdit soft-archives a suggestion. Ignored edits do NOT enter the
// rejection buffer — they are re-proposable with fresh validation.
func (s *Service) IgnoreEdit(ctx context.Context, workspaceID, editID pgtype.UUID) error {
	edit, err := s.queries.GetAgentOptEdit(ctx, db.GetAgentOptEditParams{ID: editID, WorkspaceID: workspaceID})
	if err != nil {
		return fmt.Errorf("ignore edit: %w", err)
	}
	if edit.Application == string(ApplicationIgnored) {
		return nil // idempotent
	}
	_, err = s.queries.UpdateAgentOptEditApplication(ctx, db.UpdateAgentOptEditApplicationParams{
		ID:          editID,
		Application: string(ApplicationIgnored),
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return fmt.Errorf("ignore edit: %w", err)
	}
	return nil
}

// RevertEdit rolls back an applied edit to its pre-edit snapshot (the
// "回退到上一版本" action). The reverted row is marked 'reverted' and its
// content-hash is written to the rejected buffer so it cannot be
// re-applied identically.
func (s *Service) RevertEdit(ctx context.Context, workspaceID, editID pgtype.UUID) error {
	edit, err := s.queries.GetAgentOptEdit(ctx, db.GetAgentOptEditParams{ID: editID, WorkspaceID: workspaceID})
	if err != nil {
		return fmt.Errorf("revert edit: %w", err)
	}
	if edit.Application != string(ApplicationApplied) || !edit.InstructionsSnapshot.Valid {
		return fmt.Errorf("revert edit: not an applied edit with a snapshot")
	}
	targetType := edit.TargetType
	if targetType == "" {
		targetType = SubjectAgent
	}
	if err := s.writeBackSubjectText(ctx, workspaceID, targetType, edit.TargetID, edit.InstructionsSnapshot.String); err != nil {
		return fmt.Errorf("revert edit: restore snapshot: %w", err)
	}
	_, err = s.queries.UpdateAgentOptEditApplication(ctx, db.UpdateAgentOptEditApplicationParams{
		ID:          editID,
		Application: string(ApplicationReverted),
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return fmt.Errorf("revert edit: mark reverted: %w", err)
	}
	return nil
}

// ListSuggestedEdits returns the pending-confirmation edits for a workspace
// (design-review: the 待确认建议 list). Sorted by validation score so the
// strongest suggestion is first.
func (s *Service) ListSuggestedEdits(ctx context.Context, workspaceID pgtype.UUID, limit, offset int32) ([]db.AgentOptEdit, error) {
	// 0.5.2 adversarial review d5: correction-backed suggestions (persisted
	// corrected_task_id) get the +5 ordering credit INSIDE the suggested
	// queue. The credit never affects the auto-apply gate (which reads the
	// raw score) — it only surfaces the strongest-correlated fix first.
	var credit pgtype.Numeric
	_ = credit.Scan(fmt.Sprintf("%.1f", SuggestedOrderingCredit))
	rows, err := s.queries.ListAgentOptEditsByWorkspaceAndApplication(ctx, db.ListAgentOptEditsByWorkspaceAndApplicationParams{
		WorkspaceID:     workspaceID,
		Application:     string(ApplicationSuggested),
		Limit:           limit,
		Offset:          offset,
		ValidationScore: credit,
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

var _ = pgx.ErrNoRows
