import { create } from "zustand";

type ThemeMode = "light" | "dark";

const saved = (localStorage.getItem("ragent-theme") || "light") as ThemeMode;

export const useThemeStore = create<{
  mode: ThemeMode;
  toggle: () => void;
  setMode: (m: ThemeMode) => void;
}>((set) => ({
  mode: saved,
  toggle: () =>
    set((s) => {
      const next = s.mode === "light" ? "dark" : "light";
      localStorage.setItem("ragent-theme", next);
      return { mode: next };
    }),
  setMode: (m) => {
    localStorage.setItem("ragent-theme", m);
    set({ mode: m });
  },
}));
