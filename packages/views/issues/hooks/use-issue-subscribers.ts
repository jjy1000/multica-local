"use client";

import { useCallback, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { IssueSubscriber } from "@multica/core/types";
import type {
  SubscriberAddedPayload,
  SubscriberRemovedPayload,
} from "@multica/core/types";
import { issueSubscribersOptions, issueKeys } from "@multica/core/issues/queries";
import { useToggleIssueSubscriber } from "@multica/core/issues/mutations";
import { useWSEvent, useWSReconnect } from "@multica/core/realtime";
import { useT } from "../../i18n";
import { toast } from "sonner";

export function useIssueSubscribers(issueId: string, userId?: string) {
  const qc = useQueryClient();
  const { t } = useT("issues");
  const { data: subscribers = [], isSuccess } = useQuery(
    issueSubscribersOptions(issueId),
  );

  const toggleMutation = useToggleIssueSubscriber(issueId);

  // Reconnect recovery
  useWSReconnect(
    useCallback(() => {
      qc.invalidateQueries({ queryKey: issueKeys.subscribers(issueId) });
    }, [qc, issueId]),
  );

  // --- WS event handlers ---

  useWSEvent(
    "subscriber:added",
    useCallback(
      (payload: unknown) => {
        const p = payload as SubscriberAddedPayload;
        if (p.issue_id !== issueId) return;
        qc.setQueryData<IssueSubscriber[]>(
          issueKeys.subscribers(issueId),
          (old) => {
            if (!old) return old;
            if (
              old.some(
                (s) =>
                  s.user_id === p.user_id && s.user_type === p.user_type,
              )
            )
              return old;
            return [
              ...old,
              {
                issue_id: p.issue_id,
                user_type: p.user_type as "member" | "agent",
                user_id: p.user_id,
                reason: p.reason as IssueSubscriber["reason"],
                created_at: new Date().toISOString(),
              },
            ];
          },
        );
      },
      [qc, issueId],
    ),
  );

  useWSEvent(
    "subscriber:removed",
    useCallback(
      (payload: unknown) => {
        const p = payload as SubscriberRemovedPayload;
        if (p.issue_id !== issueId) return;
        qc.setQueryData<IssueSubscriber[]>(
          issueKeys.subscribers(issueId),
          (old) =>
            old?.filter(
              (s) =>
                !(s.user_id === p.user_id && s.user_type === p.user_type),
            ),
        );
      },
      [qc, issueId],
    ),
  );

  // --- Mutations ---

  const isSubscribed = subscribers.some(
    (s) => s.user_type === "member" && s.user_id === userId,
  );
  // Both `subscribers` and `isSubscribed` come from `data ?? []`, so before the
  // query resolves they read "nobody is subscribed" — including for people who
  // are. EVERY control derived from them must gate on this rather than render
  // the default, and that means the subscriber picker too, not just the
  // subscribe button: an unchecked row for someone already subscribed sends an
  // explicit subscribe, which rewrites their reason to 'manual' and clears any
  // opt-out scope (server/pkg/db/queries/subscriber.sql). A failed query stays
  // unknown as well; only a resolved one is truth (upstream #6380 / MUL-5714).
  const subscriptionKnown = isSuccess;

  // Serializes direct toggles. Disabling the button covers the ordinary case,
  // but React Query flushes `isPending` in a microtask, so two clicks in the
  // same tick both reach a still-enabled control. Overlapping toggles are the
  // one thing useToggleIssueSubscriber's whole-list optimistic snapshot cannot
  // survive: the second call snapshots the first one's patch and, on failure,
  // rolls back to it instead of to the server's state (upstream #6380 /
  // MUL-5714).
  const toggleInFlight = useRef(false);

  // The optimistic patch in useToggleIssueSubscriber rolls itself back on
  // failure, which puts the row back exactly as it was — indistinguishable from
  // a button that never fired. Say so instead (upstream #6380 / MUL-5714).
  const toggleSubscriber = useCallback(
    (
      subUserId: string,
      userType: "member" | "agent",
      currentlySubscribed: boolean,
    ) => {
      if (toggleInFlight.current) return;
      toggleInFlight.current = true;
      toggleMutation.mutate(
        {
          userId: subUserId,
          userType,
          subscribed: currentlySubscribed,
        },
        {
          onError: () =>
            toast.error(t(($) => $.detail.subscription_update_failed)),
          // Runs after the mutation's own onSettled, so the cache has already
          // been invalidated by the time the next toggle can start.
          onSettled: () => {
            toggleInFlight.current = false;
          },
        },
      );
    },
    [toggleMutation, t],
  );

  const toggleSubscribe = useCallback(() => {
    if (userId) toggleSubscriber(userId, "member", isSubscribed);
  }, [userId, isSubscribed, toggleSubscriber]);

  return {
    subscribers,
    subscriptionKnown,
    isSubscribed,
    togglePending: toggleMutation.isPending,
    toggleSubscribe,
    toggleSubscriber,
  };
}
