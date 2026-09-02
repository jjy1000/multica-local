package execenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// envRootOwnerFile records which workspace and task an env root belongs to,
// so the GC loop can prove the daemon created a directory before deleting
// it. Configuration may point WorkspacesRoot at an arbitrary user-owned
// tree, and a directory's age or missing metadata is not proof that the
// daemon created it (MUL-6870).
const envRootOwnerFile = ".task_owner"

const (
	envRootOwnerTempPrefix = ".task_owner-"
	envRootOwnerTempSuffix = ".tmp"
)

// EnvRootOwner is written by Prepare before any task content so active and
// partially prepared roots retain authoritative identity even without
// completion metadata.
type EnvRootOwner struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	TaskID      string `json:"task_id"`
}

// WriteEnvRootOwner records authoritative workspace/task identity. The
// same-directory temp file and rename keep lock-free GC readers from
// observing a truncated JSON marker.
func WriteEnvRootOwner(envRoot, workspaceID, taskID string) error {
	path := filepath.Join(envRoot, envRootOwnerFile)
	data, err := json.Marshal(EnvRootOwner{WorkspaceID: workspaceID, TaskID: taskID})
	if err != nil {
		return fmt.Errorf("encode env root owner for %s: %w", envRoot, err)
	}

	tmp, err := os.CreateTemp(envRoot, envRootOwnerTempPrefix+"*"+envRootOwnerTempSuffix)
	if err != nil {
		return fmt.Errorf("create temp env root owner for %s: %w", envRoot, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp env root owner for %s: %w", envRoot, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp env root owner for %s: %w", envRoot, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp env root owner for %s: %w", envRoot, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace env root owner for %s: %w", envRoot, err)
	}
	return nil
}

// ReadEnvRootOwner reads both current JSON markers and legacy plain task IDs.
// An unreadable marker is an error, not an empty owner: treating it as
// unowned would hand the caller a licence to delete the very directory it
// could not identify.
func ReadEnvRootOwner(envRoot string) (*EnvRootOwner, error) {
	b, err := os.ReadFile(filepath.Join(envRoot, envRootOwnerFile))
	if errors.Is(err, os.ErrNotExist) {
		return &EnvRootOwner{}, nil
	}
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(b))
	if !strings.HasPrefix(trimmed, "{") {
		return &EnvRootOwner{TaskID: trimmed}, nil
	}
	var owner EnvRootOwner
	if err := json.Unmarshal(b, &owner); err != nil {
		return nil, err
	}
	owner.TaskID = strings.TrimSpace(owner.TaskID)
	owner.WorkspaceID = strings.TrimSpace(owner.WorkspaceID)
	return &owner, nil
}

// validTaskRootSegment reports whether a path segment under WorkspacesRoot
// matches the owner identity that predicts it. The workspace level is the
// raw workspace ID (PredictRootDir); the task level is shortID(taskID), with
// the full task ID also accepted so pre-shortID stock and fixtures validate.
func validTaskRootSegment(segment, id string, workspace bool) bool {
	if workspace {
		return segment == id
	}
	return segment == id || segment == shortID(id)
}

// ValidateEnvRootOwnerPath proves that envRoot is the two-level task root
// named by owner under workspacesRoot. GC callers use this before every
// mutation: configuration may point WorkspacesRoot at an arbitrary user-owned
// tree, and a directory's age or missing metadata is not proof that the daemon
// created it.
func ValidateEnvRootOwnerPath(workspacesRoot, envRoot string, owner EnvRootOwner) error {
	if strings.TrimSpace(workspacesRoot) == "" || strings.TrimSpace(envRoot) == "" {
		return errors.New("execenv: workspaces root and env root are required")
	}
	if owner.WorkspaceID == "" || owner.TaskID == "" {
		return errors.New("execenv: env root owner must name both workspace and task")
	}

	relative, err := filepath.Rel(workspacesRoot, envRoot)
	if err != nil {
		return fmt.Errorf("execenv: make env root relative: %w", err)
	}
	relative = filepath.Clean(relative)
	if relative == "." || filepath.IsAbs(relative) {
		return fmt.Errorf("execenv: env root %s is not a task directory below %s", envRoot, workspacesRoot)
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || parts[0] == ".." || parts[1] == ".." {
		return fmt.Errorf("execenv: env root %s is not exactly two levels below %s", envRoot, workspacesRoot)
	}
	if !validTaskRootSegment(parts[0], owner.WorkspaceID, true) {
		return fmt.Errorf("execenv: workspace directory %q does not match owner %s", parts[0], owner.WorkspaceID)
	}
	if !validTaskRootSegment(parts[1], owner.TaskID, false) {
		return fmt.Errorf("execenv: task directory %q does not match owner %s", parts[1], owner.TaskID)
	}
	return nil
}

// ValidateGCMetaOwnership proves that envRoot's position under workspacesRoot
// matches a daemon-written GC completion record. The fork began writing
// .task_owner markers with the MUL-6870 port; directories prepared before
// that carry only .gc_meta.json, which only WriteGCMeta produces — so a valid
// record that vouches for the path is ownership proof for the pre-marker
// stock. A record naming its task pins the task segment exactly; otherwise
// the segment must at least look like a daemon-generated one (shortID hex or
// a raw UUID), which human-named content never is.
func ValidateGCMetaOwnership(workspacesRoot, envRoot string, meta *GCMeta) error {
	if meta == nil || strings.TrimSpace(meta.WorkspaceID) == "" {
		return errors.New("execenv: gc meta carries no workspace identity")
	}

	relative, err := filepath.Rel(workspacesRoot, envRoot)
	if err != nil {
		return fmt.Errorf("execenv: make env root relative: %w", err)
	}
	relative = filepath.Clean(relative)
	if relative == "." || filepath.IsAbs(relative) {
		return fmt.Errorf("execenv: env root %s is not a task directory below %s", envRoot, workspacesRoot)
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || parts[0] == ".." || parts[1] == ".." {
		return fmt.Errorf("execenv: env root %s is not exactly two levels below %s", envRoot, workspacesRoot)
	}
	if !validTaskRootSegment(parts[0], strings.TrimSpace(meta.WorkspaceID), true) {
		return fmt.Errorf("execenv: workspace directory %q does not match gc meta workspace %s", parts[0], meta.WorkspaceID)
	}
	if taskID := strings.TrimSpace(meta.TaskID); taskID != "" {
		if !validTaskRootSegment(parts[1], taskID, false) {
			return fmt.Errorf("execenv: task directory %q does not match gc meta task %s", parts[1], taskID)
		}
		return nil
	}
	if !looksLikeTaskSegment(parts[1]) {
		return fmt.Errorf("execenv: task directory %q does not look like a daemon task root", parts[1])
	}
	return nil
}

// looksLikeTaskSegment reports whether a path segment could have been
// generated as shortID(taskID) (8 lowercase hex chars) or a raw UUID — the
// two shapes fork task roots have ever used. Human-named directories fail.
func looksLikeTaskSegment(segment string) bool {
	isHex := func(s string) bool {
		for _, c := range s {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
		return len(s) > 0
	}
	if len(segment) == 8 {
		return isHex(segment)
	}
	if len(segment) != 36 || segment[8] != '-' || segment[13] != '-' || segment[18] != '-' || segment[23] != '-' {
		return false
	}
	return isHex(segment[0:8]) && isHex(segment[9:13]) && isHex(segment[14:18]) &&
		isHex(segment[19:23]) && isHex(segment[24:36])
}

// CheckEnvRootResettable refuses to wipe an existing env root unless it can
// prove the directory belongs to the claiming task (owner marker), was
// daemon-made (valid completion record vouching for the path), or holds
// nothing. Prepare runs it immediately before RemoveAll: a shortID collision
// with another task's root, or a mispointed WorkspacesRoot landing on user
// content, must fail the task rather than delete what it found.
func CheckEnvRootResettable(workspacesRoot, envRoot, workspaceID, taskID string) error {
	owner, err := ReadEnvRootOwner(envRoot)
	if err != nil {
		return fmt.Errorf("execenv: read existing env owner for %s: %w", envRoot, err)
	}
	if owner.TaskID != "" {
		if owner.TaskID != taskID {
			return fmt.Errorf("execenv: env root %s belongs to task %s; refusing to reset it for task %s", envRoot, owner.TaskID, taskID)
		}
		if owner.WorkspaceID != "" && owner.WorkspaceID != workspaceID {
			return fmt.Errorf("execenv: env root %s belongs to workspace %s; refusing to reset it for task %s in workspace %s", envRoot, owner.WorkspaceID, taskID, workspaceID)
		}
		return nil
	}
	// No owner marker. A directory holding nothing costs nothing to take over.
	holds, err := envRootHoldsWork(envRoot)
	if err != nil {
		return fmt.Errorf("execenv: inspect existing env root %s: %w", envRoot, err)
	}
	if !holds {
		return nil
	}
	// Pre-marker stock: a valid completion record that vouches for the path
	// is itself daemon-authored proof of creation.
	if meta, metaErr := ReadGCMeta(envRoot); metaErr == nil && ValidateGCMetaOwnership(workspacesRoot, envRoot, meta) == nil {
		return nil
	}
	return fmt.Errorf("execenv: env root %s holds files but names no owning task; refusing to delete it", envRoot)
}

// envRootHoldsWork reports whether envRoot contains anything beyond the owner
// marker and its crash-leftover temps — that is, anything a task could lose.
// A .gc_meta.json is daemon bookkeeping but still counts as content here: a
// directory whose only evidence is an UNREADABLE meta file must not be
// adopted, and fail-closed beats precise.
func envRootHoldsWork(envRoot string) (bool, error) {
	entries, err := os.ReadDir(envRoot)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if isEnvRootBookkeeping(e.Name()) {
			continue
		}
		return true, nil
	}
	return false, nil
}

func isEnvRootBookkeeping(name string) bool {
	if name == envRootOwnerFile {
		return true
	}
	// An unpublished owner temp is a crash leftover from WriteEnvRootOwner,
	// not task content.
	return strings.HasPrefix(name, envRootOwnerTempPrefix) &&
		strings.HasSuffix(name, envRootOwnerTempSuffix)
}
