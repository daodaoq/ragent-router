import { create } from "zustand";
import {
  dashboardApi,
  type CostOverview,
  type ModelDistributionItem,
  type RecentRouteItem,
  type CostTrendPoint,
} from "../api";

interface DashboardState {
  // Data
  overview: CostOverview | null;
  modelDistribution: ModelDistributionItem[];
  recentRoutes: RecentRouteItem[];
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
      const [overview, dist, routes, trend] = await Promise.all([
        dashboardApi.getOverview(),
        dashboardApi.getModelDistribution(),
        dashboardApi.getRecentRoutes(20),
        dashboardApi.getCostTrend(7),
      ]);
      set({
        overview,
        modelDistribution: dist.items,
        recentRoutes: routes.items,
        costTrend: trend.points,
        loading: false,
      });
    } catch (err) {
      console.error("Failed to fetch dashboard data:", err);
      set({ loading: false });
    }
  },
}));
