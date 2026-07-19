// RAgent Router — 统一 API 层入口。
// 所有组件应通过此文件导入所需 API 模块。

export { request } from "./client";
export { dashboardApi } from "./dashboard";
export type { CostOverview, ModelDistributionItem, RecentRouteItem, CostTrendPoint } from "./dashboard";
export { proxyApi } from "./proxy";
export type { ProxyCurrent, ProxyHealth, ActivateResult } from "./proxy";
export { providersApi } from "./providers";
export type { ProviderItem, ProvidersResponse, CCStatus } from "./providers";
export { trafficApi } from "./traffic";
export type {
  TrafficOverview, TrafficByModelItem, TrafficRecentItem,
  TrafficTrendPoint, TrafficErrorItem, TrafficLatencyItem, TrafficStatus,
} from "./traffic";
export { monitorApi } from "./monitor";
export type { MonitorOverview, MonitorLogItem } from "./monitor";
export { setupApi } from "./setup";
export type { SetupStatus, SetupResult } from "./setup";
export { intentApi } from "./intent";
export type {
  IntentNode, IntentTreeResponse, ClassifierConfig, DefaultProvider,
  ClassifyCandidate, SwitchResult, ClassifyResult, IntentNodeRecord,
} from "./intent";
