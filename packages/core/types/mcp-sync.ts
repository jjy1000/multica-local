// MCP sync mirror types (0.5.92) — the read-only view of the user's
// Claude Code MCP servers that the server mirrors from ~/.claude.json.
// Rows are written only by the server-side sync worker: the UI has no
// edit or delete path, and a server dropped from the source flips to
// "removed" instead of disappearing.

export type McpSyncServerStatus = "synced" | "removed";

export interface McpSyncServer {
  /** Mirror key — the `mcpServers` entry name in the source file. */
  name: string;
  /**
   * Server definition in Claude Code's own shape ({type, command, args, env}
   * or {type, url, headers}). env/header VALUES are masked server-side —
   * only key names are visible.
   */
  definition: Record<string, unknown>;
  status: McpSyncServerStatus;
  first_seen_at: string;
  last_seen_at: string;
}

export interface McpSyncSnapshot {
  servers: McpSyncServer[];
  /** RFC3339; the seeded state row's epoch default means "never synced". */
  last_synced_at: string;
  /** Last sync pass failure (missing/malformed source file), "" when healthy. */
  last_error: string;
}
