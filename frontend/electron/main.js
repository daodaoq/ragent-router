const {
  app,
  BrowserWindow,
  Tray,
  Menu,
  ipcMain,
  nativeImage,
  Notification,
  dialog,
} = require("electron");
const path = require("path");
const { spawn } = require("child_process");
const fs = require("fs");
const http = require("http");

// ── Paths ─────────────────────────────────────────────────────────
const isDev = !app.isPackaged;
const APP_DATA = path.join(app.getPath("userData"), "ragent-router");
const STATE_FILE = path.join(APP_DATA, "window-state.json");
const SETTINGS_FILE = path.join(APP_DATA, "settings.json");
const ICON_PATH = path.join(__dirname, "..", "public", "icon.png");

// Ensure data directory exists
if (!fs.existsSync(APP_DATA)) fs.mkdirSync(APP_DATA, { recursive: true });

// ── State ─────────────────────────────────────────────────────────
let mainWindow = null;
let tray = null;
let backendProcess = null;
let backendOnline = false;
let backendPort = 15722;
let isQuitting = false;
let closeToTray = true;

// ── Window State Persistence ──────────────────────────────────────
function loadWindowState() {
  try {
    if (fs.existsSync(STATE_FILE)) return JSON.parse(fs.readFileSync(STATE_FILE, "utf8"));
  } catch (e) { /* ignore */ }
  return { width: 1360, height: 860, x: undefined, y: undefined, maximized: false };
}

function saveWindowState() {
  if (!mainWindow) return;
  try {
    const maximized = mainWindow.isMaximized();
    const bounds = maximized ? loadWindowState() : mainWindow.getBounds();
    const state = { ...bounds, maximized };
    fs.writeFileSync(STATE_FILE, JSON.stringify(state));
  } catch (e) { /* ignore */ }
}

// ── Settings ──────────────────────────────────────────────────────
function loadSettings() {
  try {
    if (fs.existsSync(SETTINGS_FILE)) return JSON.parse(fs.readFileSync(SETTINGS_FILE, "utf8"));
  } catch (e) { /* ignore */ }
  return { backendPort: 15722, closeToTray: true, theme: "dark", autoStartBackend: true };
}

function saveSettings(settings) {
  try {
    fs.writeFileSync(SETTINGS_FILE, JSON.stringify(settings, null, 2));
  } catch (e) { /* ignore */ }
}

// ── Backend Manager ───────────────────────────────────────────────
function getPythonPath() {
  // Try common Python locations on Windows
  const candidates = [
    path.join(process.env.LOCALAPPDATA || "", "Programs", "Python", "Python314", "python.exe"),
    path.join(process.env.LOCALAPPDATA || "", "Programs", "Python", "Python313", "python.exe"),
    path.join(process.env.LOCALAPPDATA || "", "Programs", "Python", "Python312", "python.exe"),
    "python",
    "python3",
  ];
  for (const c of candidates) {
    try {
      if (c === "python" || c === "python3") return c;
      if (fs.existsSync(c)) return c;
    } catch (e) { /* skip */ }
  }
  return "python";
}

function startBackend() {
  if (backendProcess) return;

  const settings = loadSettings();
  backendPort = settings.backendPort || 15722;

  const python = getPythonPath();
  const backendDir = isDev
    ? path.join(__dirname, "..", "..", "backend")
    : path.join(process.resourcesPath, "backend");

  console.log(`[Backend] Starting: ${python} -m uvicorn main:app --port ${backendPort}`);
  console.log(`[Backend] Directory: ${backendDir}`);

  try {
    backendProcess = spawn(python, ["-m", "uvicorn", "main:app", "--host", "0.0.0.0", "--port", String(backendPort)], {
      cwd: backendDir,
      stdio: ["ignore", "pipe", "pipe"],
      env: { ...process.env, RAGENT_DEMO_MODE: "true" },
    });

    backendProcess.stdout.on("data", (data) => {
      console.log(`[Backend] ${data.toString().trim()}`);
    });

    backendProcess.stderr.on("data", (data) => {
      console.log(`[Backend] ${data.toString().trim()}`);
    });

    backendProcess.on("close", (code) => {
      console.log(`[Backend] Process exited with code ${code}`);
      backendProcess = null;
      setBackendStatus(false);
      // Auto-restart after 3 seconds if not quitting
      if (!isQuitting) {
        setTimeout(() => {
          if (!isQuitting && !backendProcess) startBackend();
        }, 3000);
      }
    });

    backendProcess.on("error", (err) => {
      console.error(`[Backend] Failed to start: ${err.message}`);
      backendProcess = null;
      setBackendStatus(false);
    });

    // Poll for readiness — give backend time to start first
    setTimeout(checkBackendHealth, 2000);
  } catch (err) {
    console.error(`[Backend] Error: ${err.message}`);
    backendProcess = null;
    setBackendStatus(false);
  }
}

function stopBackend() {
  if (backendProcess) {
    console.log("[Backend] Stopping...");
    isQuitting = true;
    if (process.platform === "win32") {
      spawn("taskkill", ["/pid", String(backendProcess.pid), "/f", "/t"]);
    } else {
      backendProcess.kill("SIGTERM");
    }
    backendProcess = null;
  }
  setBackendStatus(false);
}

let healthCheckTimer = null;

function checkBackendHealth() {
  // Clear any pending timer to avoid stacking
  if (healthCheckTimer) {
    clearTimeout(healthCheckTimer);
    healthCheckTimer = null;
  }

  const req = http.get(`http://localhost:${backendPort}/health`, (res) => {
    let body = "";
    res.on("data", (chunk) => (body += chunk));
    res.on("end", () => {
      if (res.statusCode === 200) {
        setBackendStatus(true);
        // Keep polling every 10s to detect backend going down
        if (!isQuitting) {
          healthCheckTimer = setTimeout(checkBackendHealth, 10000);
        }
      } else {
        setBackendStatus(false);
        if (!isQuitting) {
          healthCheckTimer = setTimeout(checkBackendHealth, 2000);
        }
      }
    });
  });
  req.on("error", () => {
    setBackendStatus(false);
    if (!isQuitting) {
      healthCheckTimer = setTimeout(checkBackendHealth, 3000);
    }
  });
  req.setTimeout(5000, () => {
    req.destroy();
    setBackendStatus(false);
    if (!isQuitting) {
      healthCheckTimer = setTimeout(checkBackendHealth, 3000);
    }
  });
}

function setBackendStatus(online) {
  if (backendOnline !== online) {
    backendOnline = online;
    if (mainWindow && !mainWindow.isDestroyed()) {
      mainWindow.webContents.send("backend-status-changed", { online, port: backendPort });
    }
    if (tray) updateTrayMenu();
  }
}

// ── Window ────────────────────────────────────────────────────────
function createWindow() {
  const state = loadWindowState();

  mainWindow = new BrowserWindow({
    width: state.width,
    height: state.height,
    x: state.x,
    y: state.y,
    minWidth: 960,
    minHeight: 640,
    frame: false,
    titleBarStyle: "hidden",
    backgroundColor: "#ffffff",
    show: false,
    icon: fs.existsSync(ICON_PATH) ? ICON_PATH : undefined,
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });

  if (state.maximized) mainWindow.maximize();

  // Load content
  if (isDev) {
    mainWindow.loadURL("http://localhost:5173");
    // DevTools only open with Ctrl+Shift+I (no auto-open)
  } else {
    mainWindow.loadFile(path.join(__dirname, "..", "dist", "index.html"));
  }

  // Show when ready (no flash)
  mainWindow.once("ready-to-show", () => {
    mainWindow.show();
  });

  // Window state events
  mainWindow.on("resize", saveWindowState);
  mainWindow.on("move", saveWindowState);

  mainWindow.on("maximize", () => {
    mainWindow.webContents.send("window-maximize-change", true);
  });
  mainWindow.on("unmaximize", () => {
    mainWindow.webContents.send("window-maximize-change", false);
  });

  mainWindow.on("close", (e) => {
    if (closeToTray && !isQuitting) {
      e.preventDefault();
      mainWindow.hide();
    } else {
      saveWindowState();
    }
  });

  mainWindow.on("closed", () => {
    mainWindow = null;
  });

  // Start backend after window is created
  const settings = loadSettings();
  if (settings.autoStartBackend !== false) {
    setTimeout(startBackend, 500);
  }
}

// ── System Tray ───────────────────────────────────────────────────
function createTray() {
  // Create a simple 16x16 icon if no icon file exists
  let trayIcon;
  if (fs.existsSync(ICON_PATH)) {
    trayIcon = nativeImage.createFromPath(ICON_PATH).resize({ width: 16, height: 16 });
  } else {
    // Create a simple purple square icon
    trayIcon = nativeImage.createEmpty();
  }

  tray = new Tray(trayIcon);
  tray.setToolTip("RAgent Router");
  updateTrayMenu();

  tray.on("double-click", () => {
    if (mainWindow) {
      mainWindow.isVisible() ? mainWindow.focus() : mainWindow.show();
    }
  });
}

function updateTrayMenu() {
  if (!tray) return;

  const contextMenu = Menu.buildFromTemplate([
    {
      label: "RAgent Router v0.1",
      enabled: false,
    },
    { type: "separator" },
    {
      label: backendOnline ? "🟢 Backend Online" : "🔴 Backend Offline",
      enabled: false,
    },
    {
      label: "Show Window",
      click: () => {
        if (mainWindow) {
          mainWindow.show();
          mainWindow.focus();
        }
      },
    },
    {
      label: "Restart Backend",
      click: () => {
        stopBackend();
        isQuitting = false;
        setTimeout(startBackend, 1000);
      },
    },
    { type: "separator" },
    {
      label: "Quit",
      click: () => {
        isQuitting = true;
        closeToTray = false;
        stopBackend();
        app.quit();
      },
    },
  ]);

  tray.setContextMenu(contextMenu);
}

// ── IPC Handlers ──────────────────────────────────────────────────
function setupIPC() {
  // Window controls
  ipcMain.on("window-minimize", () => mainWindow?.minimize());
  ipcMain.on("window-maximize", () => {
    if (mainWindow?.isMaximized()) {
      mainWindow.unmaximize();
    } else {
      mainWindow?.maximize();
    }
  });
  ipcMain.on("window-close", () => {
    if (closeToTray) {
      mainWindow?.hide();
    } else {
      isQuitting = true;
      app.quit();
    }
  });

  ipcMain.handle("window-is-maximized", () => mainWindow?.isMaximized() ?? false);

  // Backend
  ipcMain.handle("get-backend-status", () => ({
    online: backendOnline,
    port: backendPort,
  }));

  ipcMain.handle("restart-backend", () => {
    stopBackend();
    isQuitting = false;
    setTimeout(startBackend, 1000);
    return { success: true };
  });

  // App info
  ipcMain.handle("get-app-info", () => ({
    version: app.getVersion(),
    platform: process.platform,
    arch: process.arch,
    electronVersion: process.versions.electron,
    nodeVersion: process.versions.node,
    dataPath: APP_DATA,
    isDev,
  }));

  // Settings
  ipcMain.handle("get-settings", () => loadSettings());
  ipcMain.handle("save-settings", (_e, settings) => {
    const merged = { ...loadSettings(), ...settings };
    saveSettings(merged);
    if (settings.closeToTray !== undefined) closeToTray = settings.closeToTray;
    if (settings.backendPort !== undefined) backendPort = settings.backendPort;
    return merged;
  });

  // Notifications
  ipcMain.handle("show-notification", (_e, { title, body }) => {
    if (Notification.isSupported()) {
      new Notification({ title, body, icon: ICON_PATH }).show();
    }
  });

  // Dialog
  ipcMain.handle("show-message-box", (_e, options) => {
    return dialog.showMessageBox(mainWindow, options);
  });

  // Open folder in file explorer
  ipcMain.handle("show-item-in-folder", (_e, folderPath) => {
    return require("electron").shell.openPath(folderPath);
  });
}

// ── App Lifecycle ─────────────────────────────────────────────────
const gotLock = app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
} else {
  app.on("second-instance", () => {
    if (mainWindow) {
      if (mainWindow.isMinimized()) mainWindow.restore();
      mainWindow.show();
      mainWindow.focus();
    }
  });

  app.whenReady().then(() => {
    setupIPC();
    createWindow();
    createTray();
  });

  app.on("window-all-closed", () => {
    // Don't quit on window close (tray support)
    if (!closeToTray) {
      stopBackend();
      app.quit();
    }
  });

  app.on("before-quit", () => {
    isQuitting = true;
    closeToTray = false;
    stopBackend();
  });

  app.on("activate", () => {
    if (mainWindow) {
      mainWindow.show();
    } else {
      createWindow();
    }
  });
}
