import { create } from "zustand";
import {
  dashboardApi,
  type CostOverview,
  type ModelDistributionItem,
  type RecentLogItem,
  type CostTrendPoint,
} from "../api";

interface DashboardState {
  // Data
  overview: CostOverview | null;
  modelDistribution: ModelDistributionItem[];
  recentRoutes: RecentLogItem[];
  costTrend: CostTrendPoint[];

  // Loading
  loading: boolean;

  // Actions
  fetchAll: () => Promise<void>;
}

export const useDashboardStore = create<DashboardState>((set) => ({
  overview: null,
  modelDistribution: [],
  recentRoutes: [],
  costTrend: [],
  loading: false,

  fetchAll: async () => {
    set({ loading: true });
    try {
      const [overviewRes, distRes, logsRes, trendRes] = await Promise.all([
        dashboardApi.getOverview(),
        dashboardApi.getModelDistribution(),
        dashboardApi.getRecentLogs(20),
        dashboardApi.getCostTrend(7),
      ]);
      set({
        overview: overviewRes?.data ?? null,
        modelDistribution: distRes?.data ?? [],
        recentRoutes: logsRes?.data ?? [],
        costTrend: trendRes?.data ?? [],
        loading: false,
      });
    } catch (err) {
      console.warn("[Dashboard] 数据获取失败:", (err as Error).message);
      set({ loading: false });
    }
  },
}));
