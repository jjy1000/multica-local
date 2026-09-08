import { describe, expect, it } from "vitest";
import {
  DEFAULT_FORECAST_ROUNDS,
  MAX_FORECAST_ROUNDS,
  parseForecastRoundsFromText,
  resolveForecastRounds,
} from "./forecast-rounds";

describe("parseForecastRoundsFromText", () => {
  it("returns null when no phrase pins a round count", () => {
    expect(parseForecastRoundsFromText("模拟推演方案")).toBeNull();
    expect(parseForecastRoundsFromText("预测下季度营收")).toBeNull();
    expect(parseForecastRoundsFromText(null)).toBeNull();
    expect(parseForecastRoundsFromText("", null)).toBeNull();
  });

  it("does not hijack unrelated 轮 phrases", () => {
    // Tight adjacency on purpose: a round count only counts when it is
    // bound to a forecast keyword. Otherwise every "分3轮讨论" issue
    // would silently spend extra LLM rounds.
    expect(parseForecastRoundsFromText("分3轮讨论推进方案")).toBeNull();
    expect(parseForecastRoundsFromText("第一轮 review 意见")).toBeNull();
    expect(parseForecastRoundsFromText("推演方案：分 3 阶段讨论")).toBeNull();
  });

  it("parses keyword-first phrases", () => {
    expect(parseForecastRoundsFromText("模拟推演方案推演5轮")).toBe(5);
    expect(parseForecastRoundsFromText("推演 3 轮验证可行性")).toBe(3);
    expect(parseForecastRoundsFromText("预测： 7 轮")).toBe(7);
    expect(parseForecastRoundsFromText("预演9轮")).toBe(9);
  });

  it("parses count-first phrases", () => {
    expect(parseForecastRoundsFromText("3轮推演一下这个方案")).toBe(3);
    expect(parseForecastRoundsFromText("5 轮模拟")).toBe(5);
  });

  it("scans the body when the title pins nothing", () => {
    expect(parseForecastRoundsFromText("方案预演", "背景如下\n请推演6轮")).toBe(6);
  });

  it("title wins over body", () => {
    expect(
      parseForecastRoundsFromText("推演2轮", "请预测 9 轮"),
    ).toBe(2);
  });

  it("first match within a string wins", () => {
    expect(parseForecastRoundsFromText("先推演2轮，再推演 9 轮")).toBe(2);
  });

  it("clamps to the server ceiling", () => {
    expect(parseForecastRoundsFromText("推演99轮")).toBe(MAX_FORECAST_ROUNDS);
  });

  it("never returns zero or negative", () => {
    // \d{1,2} can't match "-3" but "0轮" is a valid two-char match.
    expect(parseForecastRoundsFromText("推演0轮")).toBeNull();
  });
});

describe("resolveForecastRounds", () => {
  it("falls back to the 3-round default", () => {
    expect(resolveForecastRounds("模拟推演方案")).toBe(DEFAULT_FORECAST_ROUNDS);
    expect(resolveForecastRounds(null)).toBe(DEFAULT_FORECAST_ROUNDS);
  });

  it("returns the pinned count when present", () => {
    expect(resolveForecastRounds("模拟推演方案", "推演5轮")).toBe(5);
  });
});
