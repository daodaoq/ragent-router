const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("electronAPI", {
  // ── Window Controls ──────────────────────────────────────────
  minimize: () => ipcRenderer.send("window-minimize"),
  maximize: () => ipcRenderer.send("window-maximize"),
  close: () => ipcRenderer.send("window-close"),
  isMaximized: () => ipcRenderer.invoke("window-is-maximized"),
  onMaximizeChange: (callback) => {
    ipcRenderer.on("window-maximize-change", (_event, isMaximized) => callback(isMaximized));
  },

  // ── Backend ──────────────────────────────────────────────────
  getBackendStatus: () => ipcRenderer.invoke("get-backend-status"),
  restartBackend: () => ipcRenderer.invoke("restart-backend"),
  onBackendStatus: (callback) => {
    ipcRenderer.on("backend-status-changed", (_event, status) => callback(status));
  },

  // ── App Info ─────────────────────────────────────────────────
  getAppInfo: () => ipcRenderer.invoke("get-app-info"),

  // ── Settings ─────────────────────────────────────────────────
  getSettings: () => ipcRenderer.invoke("get-settings"),
  saveSettings: (settings) => ipcRenderer.invoke("save-settings", settings),

  // ── Notifications ────────────────────────────────────────────
  showNotification: (opts) => ipcRenderer.invoke("show-notification", opts),

  // ── Dialog ───────────────────────────────────────────────────
  showMessageBox: (opts) => ipcRenderer.invoke("show-message-box", opts),
  showItemInFolder: (path) => ipcRenderer.invoke("show-item-in-folder", path),

  // ── Platform ─────────────────────────────────────────────────
  platform: process.platform,
});
