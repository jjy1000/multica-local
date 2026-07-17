// Phase 1 sample predictions — covers the four horizons + a swarm split
// so the dashboard can render the split styling path. Phase 2 replaces
// this with the SSE-fed snapshot from /state/stream. Kept here (not
// hard-coded in the component) so the type narrowing is exercised
// against a realistic shape that mirrors engine/models.py.
//
// 0.3.21: sample text is now Chinese so the demo matches the live
// engine output. The persona `name` field stays the English wire
// identifier; only the user-visible title / reasoning / agent note
// are localised. The renderer maps the English `name` to a
// translated label via useT("pythia").

import type { PythiaPrediction } from "./types";

export const SAMPLE_PREDICTIONS: PythiaPrediction[] = [
  {
    id: "sample-strait-of-hormuz",
    title: "霍尔木兹海峡油轮流量周环比下降超过 15%",
    horizon: "week",
    probability: 0.62,
    reasoning:
      "两家中型航运公司的内部人 Form 4 抛售,叠加 Polymarket 上霍尔木兹扰动合约的偏移,与运费期货走阔同步出现。",
    location: "霍尔木兹海峡",
    lat: 26.5667,
    lng: 56.25,
    agents: [
      { name: "Strategist", probability: 0.7, note: "11 天内两次伊斯兰革命卫队海军演习,模式对标 2019 年扰动前夜。" },
      { name: "Economist", probability: 0.55, note: "运费期货已 price in 8% 溢价 —— 部分信号已被市场提前消化。" },
      { name: "Naturalist", probability: 0.3, note: "无气象驱动;自然观察者视角返回基准概率。" },
      { name: "Skeptic", probability: 0.4, note: "内部人抛售可能是 10b5-1 预计划;样本小,容易过度解读。" },
    ],
    base_probability: 0.55,
    prev_probability: 0.48,
    split: true,
  },
  {
    id: "sample-pacific-storm",
    title: "已命名热带风暴将在 24 小时内登陆海南",
    horizon: "24h",
    probability: 0.78,
    reasoning:
      "联合台风警报中心路径把一级飓风中心放在 06Z 时距海岸 80 公里以内;NHC 锥形区有重叠。",
    location: "海南",
    lat: 19.5667,
    lng: 109.95,
    agents: [
      { name: "Strategist", probability: 0.5, note: "疏散态势属本地层面,无地缘政治含义。" },
      { name: "Economist", probability: 0.55, note: "海南周边旅游与航运绕行,对宏观影响有限。" },
      { name: "Naturalist", probability: 0.92, note: "海温偏高 + 垂直切变弱,路径置信度高。" },
      { name: "Skeptic", probability: 0.65, note: "夜间锥形区可能东偏,把登陆点推向 200 公里以东。" },
    ],
    base_probability: 0.74,
    prev_probability: 0.71,
    split: false,
  },
  {
    id: "sample-fed-rate",
    title: "FOMC 在下次会议降息 25 个基点(而非按兵不动)",
    horizon: "month",
    probability: 0.41,
    reasoning:
      "联邦基金期货隐含概率 38%;Kalshi 合约在静默期前逐步下移。",
    location: "华盛顿特区",
    lat: 38.9072,
    lng: -77.0369,
    agents: [
      { name: "Strategist", probability: 0.35, note: "政策路径上无地缘政治驱动。" },
      { name: "Economist", probability: 0.5, note: "核心服务通胀粘性强;新增就业反弹。" },
      { name: "Naturalist", probability: 0.25, note: "不在视角范围内,回到基准概率。" },
      { name: "Skeptic", probability: 0.45, note: "市场定价 38%,我们小幅上修。" },
    ],
    base_probability: 0.38,
    prev_probability: 0.36,
    split: false,
  },
  {
    id: "sample-quake",
    title: "年内某条有人口断层发生 M≥6.0 地震",
    horizon: "year",
    probability: 0.83,
    reasoning:
      "十年基准率约 0.78;北安那托利亚断层近期微震活动抬升,估值上调。",
    location: "北安那托利亚断层",
    lat: 40.8,
    lng: 32.2,
    agents: [
      { name: "Strategist", probability: 0.7, note: "非安全视角,小幅上调。" },
      { name: "Economist", probability: 0.6, note: "伊斯坦布尔承保暴露高,次级读数。" },
      { name: "Naturalist", probability: 0.95, note: "微震抬升真实但尚不构成诊断信号。" },
      { name: "Skeptic", probability: 0.7, note: "基准率已偏高,小幅向上修正即可。" },
    ],
    base_probability: 0.79,
    prev_probability: 0.78,
    split: false,
  },
  {
    id: "sample-cyber-cve",
    title: "CISA KEV 在 7 天内收录一款被广泛部署的 VPN 设备",
    horizon: "week",
    probability: 0.34,
    reasoning:
      "本周已流出 3 份针对同一厂商系列的 PoC。",
    location: "全球(IT 外网)",
    lat: null,
    lng: null,
    agents: [
      { name: "Strategist", probability: 0.3, note: "APT 囤积利用代码模式,未必是国家行为者。" },
      { name: "Economist", probability: 0.25, note: "无市场角度。" },
      { name: "Naturalist", probability: 0.2, note: "不在视角范围内,回到基准概率。" },
      { name: "Skeptic", probability: 0.55, note: "PoC ≠ 利用;KEV 门槛较高。" },
    ],
    base_probability: 0.32,
    prev_probability: null,
    split: true,
  },
];
