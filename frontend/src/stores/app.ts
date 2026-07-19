import { create } from "zustand";

interface BackendStatus {
  online: boolean;
  port: number;
}

interface AppState {
  backendStatus: BackendStatus;
  setBackendStatus: (status: BackendStatus) => void;
}

export const useAppStore = create<AppState>((set) => ({
  backendStatus: { online: false, port: 8000 },
  setBackendStatus: (status) => set({ backendStatus: status }),
}));

// ── Initialize from Electron IPC ──────────────────────────────────

const api = window.electronAPI;

if (api) {
  api.getBackendStatus().then((s: BackendStatus) => {
    useAppStore.getState().setBackendStatus(s);
  }).catch((err: Error) => {
    console.warn("[AppStore] 获取后端状态失败:", err.message);
  });
  api.onBackendStatus((s: BackendStatus) => {
    useAppStore.getState().setBackendStatus(s);
  });
}
