// Forecast round resolution for the pythia_oracle auto-launch (0.5.104).
//
// The retired 0.3.30.3 contract hardwired every issue-triggered
// deliberation to 10 rounds (~45s + 10 LLM calls per issue). Now the
// issue's own text may pin the round count — a title/body phrase like
// "推演5轮" / "3轮推演" / "模拟 8 轮" — and anything unpinned falls
// back to a lean 3-round default. The server still clamps the final
// number at 10; parsing here keeps to the same ceiling so the UI never
// sends a value the server would silently trim.

/** Default deliberation rounds when the issue text pins none. Matches
 *  defaultIssueForecastRounds in server/internal/handler/forecast_issue.go. */
export const DEFAULT_FORECAST_ROUNDS = 3;

/** Hard ceiling — mirrors maxIssueForecastRounds on the server. */
export const MAX_FORECAST_ROUNDS = 10;

// Keyword + count in either order, digits only, optional space between
// the parts. The keyword is a non-capturing group so the count is
// ALWAYS capture 1 regardless of which pattern matched. Deliberately
// tight adjacency: "分3轮讨论" or "第一轮" must NOT hijack the round
// count just because 推演 appears elsewhere in the title — a false
// positive silently spends more LLM rounds than the user asked for,
// which is the exact failure mode this helper replaces.
const KEYWORD = "(?:推演|预测|模拟|预演)";
const COUNT = "(\\d{1,2})";
const KEYWORD_FIRST = new RegExp(`${KEYWORD}\\s*[：:]?\\s*${COUNT}\\s*轮`);
const COUNT_FIRST = new RegExp(`${COUNT}\\s*轮\\s*${KEYWORD}`);

/**
 * Extract a caller-pinned round count from the issue's title/body.
 * Returns null when no phrase pins one. The title is scanned before the
 * body; within one string the first match wins. Pure function — unit
 * pinned in forecast-rounds.test.ts.
 */
export function parseForecastRoundsFromText(
  title: string | null | undefined,
  body?: string | null,
): number | null {
  for (const text of [title, body]) {
    if (!text) continue;
    const m = KEYWORD_FIRST.exec(text) ?? COUNT_FIRST.exec(text);
    const raw = m?.[1];
    if (!raw) continue;
    const n = Number.parseInt(raw, 10);
    if (Number.isFinite(n) && n > 0) {
      return Math.min(MAX_FORECAST_ROUNDS, n);
    }
  }
  return null;
}

/** Parse-or-default form for trigger call sites. */
export function resolveForecastRounds(
  title: string | null | undefined,
  body?: string | null,
): number {
  return parseForecastRoundsFromText(title, body) ?? DEFAULT_FORECAST_ROUNDS;
}
