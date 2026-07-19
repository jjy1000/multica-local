import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const autopilotKeys = {
  all: (wsId: string) => ["autopilots", wsId] as const,
  list: (wsId: string) => [...autopilotKeys.all(wsId), "list"] as const,
  detail: (wsId: string, id: string) =>
    [...autopilotKeys.all(wsId), "detail", id] as const,
  runs: (wsId: string, id: string) =>
    [...autopilotKeys.all(wsId), "runs", id] as const,
  run: (wsId: string, autopilotId: string, runId: string) =>
    [...autopilotKeys.all(wsId), "runs", autopilotId, runId] as const,
  deliveries: (wsId: string, id: string) =>
    [...autopilotKeys.all(wsId), "deliveries", id] as const,
  delivery: (wsId: string, autopilotId: string, deliveryId: string) =>
    [...autopilotKeys.all(wsId), "deliveries", autopilotId, deliveryId] as const,
};

export function autopilotListOptions(wsId: string) {
  return queryOptions({
    queryKey: autopilotKeys.list(wsId),
    queryFn: () => api.listAutopilots(),
    select: (data) => data.autopilots,
  });
}

export function autopilotDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: autopilotKeys.detail(wsId, id),
    queryFn: () => api.getAutopilot(id),
  });
}

export function autopilotRunsOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: autopilotKeys.runs(wsId, id),
    queryFn: () => api.listAutopilotRuns(id),
    select: (data) => data.runs,
    // 0.3.45.8 (P0#3.7 sibling): the run list is invalidated by the WS
    // `autopilot:run_start` / `autopilot:run_done` events, but that push is
    // the only freshness signal — a dropped event left an in-flight run
    // stuck on "running" until the user refocused the window (same class
    // as the pre-0.3.45.7 agent-task-snapshot bug). Poll every 5s only
    // while a run is actually in flight; when everything is terminal the
    // list falls back to WS + focus with zero idle polling. `query.state.data`
    // is the raw (pre-select) response, hence `.runs`.
    refetchInterval: (query) =>
      (query.state.data?.runs ?? []).some((r) => r.status === "running")
        ? 5 * 1000
        : false,
  });
}

// autopilotRunOptions fetches a single run with its full trigger_payload.
// The list endpoint (autopilotRunsOptions) omits trigger_payload to keep
// list responses small; callers (e.g. the run-detail dialog) use this
// query on demand when the user opens a run.
export function autopilotRunOptions(
  wsId: string,
  autopilotId: string,
  runId: string,
  options?: { enabled?: boolean },
) {
  return queryOptions({
    queryKey: autopilotKeys.run(wsId, autopilotId, runId),
    queryFn: () => api.getAutopilotRun(autopilotId, runId),
    enabled: options?.enabled ?? true,
  });
}

// autopilotDeliveriesOptions powers the Deliveries section in the autopilot
// detail page. The list is slim — raw_body / selected_headers / response_body
// are omitted server-side. Detail rows are fetched on-demand when the user
// expands a row (see autopilotDeliveryOptions).
export function autopilotDeliveriesOptions(
  wsId: string,
  autopilotId: string,
  options?: { enabled?: boolean },
) {
  return queryOptions({
    queryKey: autopilotKeys.deliveries(wsId, autopilotId),
    queryFn: () => api.listAutopilotDeliveries(autopilotId),
    select: (data) => data.deliveries,
    enabled: options?.enabled ?? true,
  });
}

// autopilotDeliveryOptions fetches the full delivery row including raw_body
// and headers subset. Used by the detail dialog opened from a list row.
export function autopilotDeliveryOptions(
  wsId: string,
  autopilotId: string,
  deliveryId: string,
  options?: { enabled?: boolean },
) {
  return queryOptions({
    queryKey: autopilotKeys.delivery(wsId, autopilotId, deliveryId),
    queryFn: () => api.getAutopilotDelivery(autopilotId, deliveryId),
    enabled: options?.enabled ?? true,
  });
}
