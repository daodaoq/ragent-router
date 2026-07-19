// 全局类型声明

interface BackendStatus {
  online: boolean;
  port: number;
}

interface AppInfo {
  version: string;
  platform: string;
  electron: string;
  nodejs: string;
  dataPath: string;
}

interface SettingsData {
  backendPort: number;
  closeToTray: boolean;
  autoStartBackend: boolean;
}

interface ElectronAPI {
  // 后端状态
  getBackendStatus: () => Promise<BackendStatus>;
  onBackendStatus: (callback: (s: BackendStatus) => void) => void;

  // 设置
  getSettings: () => Promise<SettingsData>;
  saveSettings: (s: SettingsData) => Promise<void>;

  // 应用信息
  getAppInfo: () => Promise<AppInfo>;
  showItemInFolder: (path: string) => void;

  // 窗口控制
  minimize: () => void;
  maximize: () => void;
  close: () => void;
  isMaximized: () => Promise<boolean>;
  onMaximizeChange: (callback: (v: boolean) => void) => void;

  // 后端管理
  restartBackend: () => Promise<void>;
}

declare global {
  interface Window {
    electronAPI?: ElectronAPI;
  }
}

export {};
