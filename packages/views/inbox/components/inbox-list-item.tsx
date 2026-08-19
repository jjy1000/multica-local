"use client";

import { StatusIcon } from "../../issues/components";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import { ActorAvatar } from "../../common/actor-avatar";
import { Archive } from "lucide-react";
import type { InboxItem } from "@multica/core/types";
import { InboxDetailLabel } from "./inbox-detail-label";
import { getInboxDisplayTitle } from "./inbox-display";
import { useStatusLabel } from "../../issues/utils/status-label";
import { useT } from "../../i18n";

// Hook returning a localized relative-time formatter — the i18n equivalent
// of the previous static `timeAgo` function. Returning a function (rather
// than a string) keeps call-site usage identical: `timeAgo(dateStr)`.
export function useTimeAgo() {
  const { t } = useT("inbox");
  return (dateStr: string): string => {
    const diff = Date.now() - new Date(dateStr).getTime();
    const minutes = Math.floor(diff / 60000);
    if (minutes < 1) return t(($) => $.list.time.just_now);
    if (minutes < 60) return t(($) => $.list.time.minutes, { count: minutes });
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return t(($) => $.list.time.hours, { count: hours });
    const days = Math.floor(hours / 24);
    return t(($) => $.list.time.days, { count: days });
  };
}

export function InboxListItem({
  item,
  isSelected,
  onClick,
  onArchive,
}: {
  item: InboxItem;
  isSelected: boolean;
  onClick: () => void;
  onArchive: () => void;
}) {
  const { t } = useT("inbox");
  const timeAgo = useTimeAgo();
  const displayTitle = getInboxDisplayTitle(item);
  // The glyph is per CATEGORY, so it alone cannot tell "In Review" from a
  // custom "Human Review" — moving between two statuses of the same category
  // left this row pixel-identical and read as "the inbox never updated"
  // (MUL-6395). Colour is what carries a custom status's own identity, exactly
  // as the status-changed detail label already renders it. Built-ins pass null
  // so they keep their semantic token colour rather than the catalog's seed.
  const { categoryOf: statusCategoryOf, entryOf: statusEntryOf } =
    useIssueStatuses(item.workspace_id);
  const statusLabelOf = useStatusLabel(item.workspace_id);
  const statusEntry = item.issue_status
    ? statusEntryOf(item.issue_status)
    : undefined;
  const statusColor =
    statusEntry?.is_system === true ? null : statusEntry?.color;

  return (
    <button
      type="button"
      onClick={onClick}
      className={`group flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors ${
        isSelected ? "bg-accent" : "hover:bg-accent/50"
      }`}
    >
      <ActorAvatar
        actorType={item.actor_type ?? item.recipient_type}
        actorId={item.actor_id ?? item.recipient_id}
        size={28}
        enableHoverCard
      />
      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-1.5">
            {!item.read && (
              <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-brand" />
            )}
            <span
              className={`truncate text-body ${!item.read ? "font-medium" : "text-muted-foreground"}`}
            >
              {displayTitle}
            </span>
          </div>
          <div className="flex shrink-0 items-center gap-1">
            <span
              role="button"
              tabIndex={-1}
              title={t(($) => $.list.archive_tooltip)}
              onClick={(e) => {
                e.stopPropagation();
                onArchive();
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.stopPropagation();
                  onArchive();
                }
              }}
              className="hidden rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground group-hover:inline-flex"
            >
              <Archive className="h-3.5 w-3.5" />
            </span>
            {item.issue_status && (
              // Icon-only, like every other issue row — but a colour is not a
              // name, and this row has no space for the CustomStatusChip the
              // board card and list row carry. `title` is that name, and it is
              // the same affordance the archive button above already uses.
              <span
                title={statusLabelOf(item.issue_status)}
                className="flex shrink-0 items-center"
              >
                <StatusIcon
                  status={item.issue_status}
                  category={statusCategoryOf(item.issue_status)}
                  color={statusColor}
                  className="h-3.5 w-3.5 shrink-0"
                />
              </span>
            )}
          </div>
        </div>
        <div className="mt-0.5 flex items-center justify-between gap-2">
          <p className={`min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-caption ${item.read ? "text-muted-foreground/60" : "text-muted-foreground"}`}>
            <InboxDetailLabel item={item} />
          </p>
          <span className={`shrink-0 text-caption ${item.read ? "text-muted-foreground/60" : "text-muted-foreground"}`}>
            {timeAgo(item.created_at)}
          </span>
        </div>
      </div>
    </button>
  );
}
