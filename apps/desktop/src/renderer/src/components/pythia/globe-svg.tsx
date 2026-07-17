// GlobeSVG — equirectangular SVG world map + forecast rings.
//
// Phase 1 ships without a world coastlines layer: the graticule +
// continent silhouettes drawn inline are enough to anchor predictions.
// Phase 2/3 can swap the silhouette set for a vendored topojson
// (world-110m.json, ~80KB) without changing the projection API.
//
// Layout: 720 × 360 viewBox. equirectangular: x = (lng + 180)/360 * w,
// y = (90 - lat)/180 * h. Distortion at the poles is acceptable for
// a status surface — we are not navigating, just dropping pins.

import { useMemo } from "react";
import type { PythiaPrediction } from "./types";
import { useT } from "@multica/views/i18n";

const W = 720;
const H = 360;

// Coarse continent silhouettes — a "minimal land" set that fits inside
// 4KB of source. Coordinates are coarse lon/lat; rendered through the
// equirectangular projection so no GeoJSON runtime is needed.
const CONTINENT_SILHOUETTES: ReadonlyArray<ReadonlyArray<readonly [number, number]>> = [
  // North America
  [
    [-168, 65], [-160, 70], [-140, 70], [-128, 70], [-115, 73], [-100, 73],
    [-85, 73], [-75, 78], [-65, 75], [-55, 65], [-50, 55], [-60, 47],
    [-66, 44], [-70, 41], [-75, 38], [-78, 34], [-80, 32], [-83, 30],
    [-85, 30], [-88, 30], [-92, 29], [-95, 29], [-97, 26], [-98, 22],
    [-105, 20], [-110, 24], [-115, 30], [-118, 34], [-122, 37], [-124, 40],
    [-124, 47], [-128, 53], [-135, 58], [-145, 60], [-155, 60], [-163, 55],
    [-168, 65],
  ],
  // South America
  [
    [-78, 12], [-70, 12], [-60, 10], [-52, 5], [-50, 0], [-48, -5],
    [-40, -8], [-35, -10], [-38, -22], [-45, -28], [-52, -33], [-58, -38],
    [-65, -42], [-70, -52], [-72, -55], [-75, -50], [-72, -42], [-72, -30],
    [-71, -20], [-72, -15], [-75, -10], [-78, -5], [-78, 0], [-78, 12],
  ],
  // Europe
  [
    [-10, 36], [-5, 36], [0, 40], [5, 43], [10, 44], [15, 45],
    [20, 40], [25, 36], [30, 38], [35, 43], [40, 45], [40, 55],
    [35, 60], [30, 65], [25, 68], [20, 70], [10, 68], [5, 62],
    [0, 58], [-5, 56], [-8, 54], [-10, 50], [-10, 36],
  ],
  // Africa
  [
    [-17, 21], [-15, 14], [-10, 8], [-5, 5], [0, 5], [5, 5],
    [10, 2], [15, 0], [20, -5], [25, -15], [30, -20], [35, -25],
    [38, -32], [35, -30], [30, -25], [25, -15], [18, -5], [12, 5],
    [5, 12], [-5, 15], [-12, 18], [-17, 21],
  ],
  // Asia (rough: skips the islands)
  [
    [25, 38], [35, 38], [45, 40], [55, 38], [60, 35], [65, 30],
    [70, 30], [75, 33], [80, 30], [85, 27], [90, 25], [95, 22],
    [100, 20], [105, 18], [108, 12], [115, 10], [120, 15], [122, 22],
    [120, 30], [125, 35], [130, 42], [135, 50], [140, 53], [145, 55],
    [150, 58], [155, 60], [160, 62], [170, 65], [180, 68], [180, 75],
    [170, 75], [140, 75], [110, 75], [80, 75], [60, 72], [50, 70],
    [40, 65], [30, 60], [25, 50], [25, 38],
  ],
  // Australia
  [
    [115, -22], [120, -18], [130, -12], [138, -14], [145, -16],
    [150, -22], [153, -28], [148, -36], [140, -38], [130, -32],
    [120, -32], [115, -28], [115, -22],
  ],
  // Greenland
  [
    [-50, 60], [-30, 60], [-20, 65], [-15, 75], [-25, 82], [-40, 82],
    [-55, 78], [-58, 70], [-55, 62], [-50, 60],
  ],
];

function _projectImpl(lng: number, lat: number, w: number, h: number): readonly [number, number] {
  const x = ((lng + 180) / 360) * w;
  const y = ((90 - lat) / 180) * h;
  return [x, y];
}

function project(lng: number, lat: number): readonly [number, number] {
  return _projectImpl(lng, lat, W, H);
}

/**
 * Equirectangular projection — exposed for unit tests.
 * Pinned at the component's viewBox dimensions so callers can
 * reason about expected pixel positions without instantiating the
 * SVG component.
 */
export function projectLngLat(
  lng: number,
  lat: number,
): readonly [number, number] {
  return _projectImpl(lng, lat, W, H);
}

function polygonToPath(poly: ReadonlyArray<readonly [number, number]>): string {
  return (
    poly
      .map(([lng, lat], idx) => {
        const [x, y] = project(lng, lat);
        return `${idx === 0 ? "M" : "L"}${x.toFixed(1)} ${y.toFixed(1)}`;
      })
      .join(" ") + " Z"
  );
}

const HORIZON_FILL: Record<PythiaPrediction["horizon"], string> = {
  "24h": "rgb(248 113 113)", // red-400
  week: "rgb(251 191 36)", // amber-400
  month: "rgb(74 222 128)", // green-400
  year: "rgb(96 165 250)", // blue-400
};

// Persona fill set — used when `accentByPersona` is on. Matches the
// bars in <SwarmVoteBars /> so a split prediction picks up its loudest
// voter's colour around the globe.
export const PERSONA_FILL: Record<string, string> = {
  Strategist: "rgb(56 189 248)", // sky-400
  Economist: "rgb(251 191 36)", // amber-400
  Naturalist: "rgb(52 211 153)", // emerald-400
  Skeptic: "rgb(232 121 249)", // fuchsia-400
};

// leadingPersona returns the persona with the highest per-forecast
// probability. Falls back to "Skeptic" when the prediction has no
// agents[] (ephemeral what-if), so the ring still gets a colour.
//
// Exported for unit tests.
export function leadingPersona(p: PythiaPrediction): string {
  if (p.agents.length === 0) return "Skeptic";
  let best = p.agents[0];
  for (const a of p.agents) {
    if (a.probability > best.probability) best = a;
  }
  return best.name;
}

export interface GlobeSVGProps {
  predictions: ReadonlyArray<PythiaPrediction>;
  selectedId: string | null;
  onSelect: (id: string) => void;
  /**
   * When true, the ring fill uses the dominant persona's colour
   * (matches SwarmVoteBars); the stroke still encodes the horizon.
   * Off by default so the default visual stays horizon-driven.
   */
  accentByPersona?: boolean;
}

export function GlobeSVG({
  predictions,
  selectedId,
  onSelect,
  accentByPersona = false,
}: GlobeSVGProps) {
  const { t } = useT("pythia");
  const continentPaths = useMemo(
    () => CONTINENT_SILHOUETTES.map(polygonToPath),
    [],
  );

  const graticuleLines = useMemo(() => {
    const lines: { x1: number; y1: number; x2: number; y2: number }[] = [];
    for (let lng = -150; lng <= 150; lng += 30) {
      const [x] = project(lng, 0);
      lines.push({ x1: x, y1: 0, x2: x, y2: H });
    }
    for (let lat = -60; lat <= 60; lat += 30) {
      const [, y] = project(0, lat);
      lines.push({ x1: 0, y1: y, x2: W, y2: y });
    }
    return lines;
  }, []);

  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      className="h-full w-full rounded-lg border border-border bg-slate-950"
      role="img"
      aria-label={t(($) => $.pythia_forecast_globe_aria)}
    >
      <defs>
        <radialGradient id="globe-vignette" cx="50%" cy="50%" r="65%">
          <stop offset="60%" stopColor="rgb(2 6 23 / 0)" />
          <stop offset="100%" stopColor="rgb(2 6 23 / 0.55)" />
        </radialGradient>
      </defs>

      {/* continents */}
      <g>
        {continentPaths.map((d, idx) => (
          <path
            key={`continent-${idx}`}
            d={d}
            fill="rgb(30 41 59)" // slate-800
            stroke="rgb(51 65 85)" // slate-700
            strokeWidth={0.5}
          />
        ))}
      </g>

      {/* graticule */}
      <g stroke="rgb(51 65 85 / 0.5)" strokeWidth={0.3}>
        {graticuleLines.map((l, idx) => (
          <line key={`grat-${idx}`} {...l} />
        ))}
      </g>

      {/* vignette */}
      <rect width={W} height={H} fill="url(#globe-vignette)" />

      {/* forecast rings */}
      <g>
        {predictions
          .filter((p): p is PythiaPrediction & { lat: number; lng: number } =>
            p.lat !== null && p.lng !== null,
          )
          .map((p) => {
            const [cx, cy] = project(p.lng, p.lat);
            const baseR = 4 + p.probability * 16;
            const isSelected = p.id === selectedId;
            const horizonColor = HORIZON_FILL[p.horizon];
            const fill = accentByPersona
              ? (PERSONA_FILL[leadingPersona(p)] ?? horizonColor)
              : horizonColor;
            return (
              <g key={p.id}>
                {isSelected && (
                  <>
                    {/* Outer halo: gives the selected ring a clear
                        "this is what you're looking at" affordance
                        without losing the pulse animation underneath. */}
                    <circle
                      cx={cx}
                      cy={cy}
                      r={baseR + 4}
                      fill="none"
                      stroke="white"
                      strokeOpacity={0.85}
                      strokeWidth={1}
                    />
                    <circle
                      cx={cx}
                      cy={cy}
                      r={baseR + 8}
                      fill="none"
                      stroke="white"
                      strokeOpacity={0.25}
                      strokeWidth={0.5}
                      className="animate-pythia-pulse-ring"
                    />
                  </>
                )}
                <circle
                  cx={cx}
                  cy={cy}
                  r={baseR}
                  fill="none"
                  stroke={horizonColor}
                  strokeOpacity={p.split ? 0.95 : 0.55}
                  strokeWidth={p.split ? 1.5 : 1}
                  className="animate-pythia-pulse-ring"
                />
                <circle
                  cx={cx}
                  cy={cy}
                  r={Math.max(2.5, baseR * 0.32)}
                  fill={fill}
                  stroke={isSelected ? "white" : "transparent"}
                  strokeWidth={isSelected ? 2 : 0}
                  className="cursor-pointer"
                  onClick={() => onSelect(p.id)}
                >
                  <title>{`${p.title} — ${(p.probability * 100).toFixed(0)}%`}</title>
                </circle>
              </g>
            );
          })}
      </g>
    </svg>
  );
}

// Re-export the horizon palette so other dashboard pieces can stay
// consistent without re-declaring.
export const PYTHIA_HORIZON_FILL = HORIZON_FILL;