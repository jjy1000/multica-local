"use client";

// CausalGraphCanvas (0.5.86) — zoom/pan wrapper around CausalMinimap for
// the FULL-PAGE /experimental/causal-graph surface. The issue-header
// popover stays a fixed-size surface and keeps using the bare minimap.
//
//   - Wheel zoom tracks the cursor: wheelZoomFactor normalises
//     line/page/pixel deltas and zoomByAt pins the point under the
//     cursor. The listener is native + non-passive because React's
//     synthetic onWheel is passive at the root and could not
//     preventDefault.
//   - Background pointer-drag pans, with the same 5px click-vs-drag
//     threshold as use-board-drag-pan.ts. Node dragging always wins:
//     the minimap only reports presses whose target is NOT a node, so
//     the two gestures are mutually exclusive by ORIGIN.
//   - The transform lives in an inner <g> of the minimap svg (see
//     CausalMinimap.viewportTransform), so the base w-full CSS scaling
//     is untouched and node drags stay exact — the drag conversion
//     reads the content group's screen CTM, which includes this
//     transform.
//
// Reset is identity, not computeFitTransform: the ring layout is
// already computed to fill the viewBox and the svg CSS-scales to its
// container, so identity IS the fitted view.

import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { Maximize, Minus, Plus } from "lucide-react";
import type { CausalEdge, CausalNode } from "@multica/core/types/api";
import {
  panBy,
  wheelZoomFactor,
  zoomByAt,
  zoomByAtCenter,
  type ZoomTransform,
} from "../../editor/utils/zoom-transform";
import {
  CausalMinimap,
  type CausalPositionOverride,
  type CausalViewportTransform,
} from "./causal-minimap";

// Matches the zoom-transform.ts button-step cadence (~4 presses per doubling).
const CANVAS_ZOOM_STEP = 1.2;
// Same click-vs-drag threshold as use-board-drag-pan.ts.
const PAN_ACTIVATION_DISTANCE = 5;

export function CausalGraphCanvas({
  nodes,
  edges,
  width = 760,
  height = 480,
  selectedNodeId,
  onSelectNode,
  positionOverrides,
  onPositionOverride,
  onDragStateChange,
  labels,
}: {
  nodes: CausalNode[];
  edges: CausalEdge[];
  width?: number;
  height?: number;
  selectedNodeId?: string | null;
  onSelectNode?: (node: CausalNode) => void;
  /** Session-only drag overrides owned by the parent page. */
  positionOverrides?: Map<string, CausalPositionOverride>;
  onPositionOverride?: (nodeId: string, pos: CausalPositionOverride | null) => void;
  onDragStateChange?: (dragging: boolean) => void;
  /** Localised aria-labels for the corner controls (English defaults). */
  labels?: { zoomIn?: string; zoomOut?: string; resetView?: string };
}) {
  const wrapperRef = useRef<HTMLDivElement | null>(null);
  const [transform, setTransform] = useState<ZoomTransform>({ scale: 1, x: 0, y: 0 });
  const panRef = useRef<{
    pointerId: number;
    startX: number;
    startY: number;
    lastX: number;
    lastY: number;
    active: boolean;
  } | null>(null);

  useEffect(() => {
    const el = wrapperRef.current;
    if (!el) return;
    const onWheel = (event: WheelEvent) => {
      event.preventDefault();
      const rect = el.getBoundingClientRect();
      const anchor = { x: event.clientX - rect.left, y: event.clientY - rect.top };
      setTransform((t) =>
        zoomByAt(
          t,
          wheelZoomFactor(event.deltaY, event.deltaMode),
          anchor,
          { width, height },
          { width: rect.width, height: rect.height },
        ),
      );
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, [width, height]);

  const endPan = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
    const pan = panRef.current;
    if (!pan || event.pointerId !== pan.pointerId) return;
    panRef.current = null;
    const el = wrapperRef.current;
    if (el) {
      el.style.removeProperty("cursor");
      el.style.removeProperty("user-select");
      try {
        el.releasePointerCapture(event.pointerId);
      } catch {
        /* capture already released */
      }
    }
  }, []);

  const handlePanMove = useCallback(
    (event: ReactPointerEvent<HTMLDivElement>) => {
      const pan = panRef.current;
      if (!pan || event.pointerId !== pan.pointerId) return;
      const el = wrapperRef.current;
      if (!el) return;
      // The primary button was released somewhere we didn't hear about.
      if ((event.buttons & 1) === 0) {
        panRef.current = null;
        el.style.removeProperty("cursor");
        el.style.removeProperty("user-select");
        return;
      }
      if (!pan.active) {
        const dist = Math.hypot(event.clientX - pan.startX, event.clientY - pan.startY);
        if (dist < PAN_ACTIVATION_DISTANCE) return;
        pan.active = true;
        el.style.cursor = "grabbing";
      }
      const dx = event.clientX - pan.lastX;
      const dy = event.clientY - pan.lastY;
      pan.lastX = event.clientX;
      pan.lastY = event.clientY;
      const rect = el.getBoundingClientRect();
      setTransform((t) =>
        panBy(t, dx, dy, { width, height }, { width: rect.width, height: rect.height }),
      );
    },
    [width, height],
  );

  const handleBackgroundPointerDown = useCallback((event: ReactPointerEvent<SVGSVGElement>) => {
    if (event.pointerType === "mouse" && event.button !== 0) return;
    const el = wrapperRef.current;
    if (!el) return;
    event.preventDefault();
    el.style.userSelect = "none";
    try {
      el.setPointerCapture(event.pointerId);
    } catch {
      // Capture unsupported — bubbled pointer events still drive the pan.
    }
    panRef.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      lastX: event.clientX,
      lastY: event.clientY,
      active: false,
    };
  }, []);

  const zoomStep = (factor: number) => {
    const el = wrapperRef.current;
    const viewport = { width: el?.clientWidth ?? width, height: el?.clientHeight ?? height };
    setTransform((t) => zoomByAtCenter(t, factor, { width, height }, viewport));
  };

  const viewport: CausalViewportTransform = { x: transform.x, y: transform.y, scale: transform.scale };

  return (
    <div
      ref={wrapperRef}
      className="relative overflow-hidden rounded-md"
      onPointerMove={handlePanMove}
      onPointerUp={endPan}
      onPointerCancel={endPan}
      onLostPointerCapture={endPan}
    >
      <CausalMinimap
        nodes={nodes}
        edges={edges}
        width={width}
        height={height}
        selectedNodeId={selectedNodeId}
        onSelectNode={onSelectNode}
        positionOverrides={positionOverrides}
        onPositionOverride={onPositionOverride}
        onDragStateChange={onDragStateChange}
        viewportTransform={viewport}
        onBackgroundPointerDown={handleBackgroundPointerDown}
      />
      <div className="absolute right-2 top-2 flex flex-col gap-1">
        <button
          type="button"
          aria-label={labels?.zoomIn ?? "Zoom in"}
          onClick={() => zoomStep(CANVAS_ZOOM_STEP)}
          className="flex size-6 items-center justify-center rounded-md border border-border/60 bg-background/90 text-muted-foreground shadow-sm backdrop-blur hover:bg-accent hover:text-foreground"
        >
          <Plus className="size-3.5" aria-hidden />
        </button>
        <button
          type="button"
          aria-label={labels?.zoomOut ?? "Zoom out"}
          onClick={() => zoomStep(1 / CANVAS_ZOOM_STEP)}
          className="flex size-6 items-center justify-center rounded-md border border-border/60 bg-background/90 text-muted-foreground shadow-sm backdrop-blur hover:bg-accent hover:text-foreground"
        >
          <Minus className="size-3.5" aria-hidden />
        </button>
        <button
          type="button"
          aria-label={labels?.resetView ?? "Reset view"}
          onClick={() => setTransform({ scale: 1, x: 0, y: 0 })}
          className="flex size-6 items-center justify-center rounded-md border border-border/60 bg-background/90 text-muted-foreground shadow-sm backdrop-blur hover:bg-accent hover:text-foreground"
        >
          <Maximize className="size-3.5" aria-hidden />
        </button>
      </div>
    </div>
  );
}
