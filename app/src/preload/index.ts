import { contextBridge, ipcRenderer } from "electron";
import type {
  ConnectionState,
  DaemonEvent,
  KeyboardCaptureEvent,
  PadmateAPI,
  UIResult,
} from "../shared/api";
import type { UpdateState } from "../shared/update";

const eventListeners = new Set<(event: DaemonEvent) => void>();
ipcRenderer.on(
  "padmate:eventBatch",
  (_event, batch: { id: number; events: DaemonEvent[] }) => {
    try {
      for (const event of batch.events)
        for (const listener of eventListeners) listener(event);
    } finally {
      ipcRenderer.send("padmate:eventAck", batch.id);
    }
  },
);

function listen<T>(channel: string, listener: (value: T) => void): () => void {
  const callback = (_event: Electron.IpcRendererEvent, value: T) =>
    listener(value);
  ipcRenderer.on(channel, callback);
  return () => ipcRenderer.removeListener(channel, callback);
}

async function invoke<T>(channel: string, ...args: unknown[]): Promise<T> {
  const result = (await ipcRenderer.invoke(channel, ...args)) as UIResult<T>;
  // contextBridge preserves rejected plain data, but strips custom Error
  // properties. Keep diagnostics and revision conflicts intact for the editor.
  if (!result.ok) return Promise.reject(result.error);
  return result.value;
}

const api: PadmateAPI = {
  getConnection: () => ipcRenderer.invoke("padmate:connection"),
  onConnection: (listener) =>
    listen<ConnectionState>("padmate:connectionChanged", listener),
  getStatus: () => ipcRenderer.invoke("padmate:status"),
  getDevices: () => ipcRenderer.invoke("padmate:devices"),
  identifyDevice: (key) => invoke("padmate:identifyDevice", key),
  rawWatchStart: (device) => invoke("padmate:rawWatchStart", device),
  rawWatchStop: (device) => invoke("padmate:rawWatchStop", device),
  setMapping: (device, mapping) =>
    invoke("padmate:mappingSet", device, mapping),
  resetMapping: (device) => invoke("padmate:mappingReset", device),
  pause: () => ipcRenderer.invoke("padmate:pause"),
  resume: () => ipcRenderer.invoke("padmate:resume"),
  releaseAll: () => ipcRenderer.invoke("padmate:releaseAll"),
  getConfig: () => ipcRenderer.invoke("padmate:configGet"),
  listProfiles: () => invoke("padmate:profilesList"),
  getProfile: (id) => invoke("padmate:profileGet", id),
  resolveProfile: (id) => invoke("padmate:profileResolve", id),
  putProfile: (id, text, revision) =>
    invoke("padmate:profilePut", id, text, revision),
  validateProfile: (text) => invoke("padmate:profileValidate", text),
  activateProfile: (id) => invoke("padmate:profileActivate", id),
  deleteProfile: (id, revision) =>
    invoke("padmate:profileDelete", id, revision),
  copyProfile: (from, to) => invoke("padmate:profileCopy", from, to),
  exportProfile: (id, name) => invoke("padmate:profileExport", id, name),
  importProfile: (options) => invoke("padmate:profileImport", options ?? null),
  getRecentApps: () => invoke("padmate:appsRecent"),
  getAppIcon: (path) => invoke("padmate:appIcon", path),
  resolveAppPath: (exe) => invoke("padmate:appPath", exe),
  listInstalledApps: () => invoke("padmate:appsInstalled"),
  browseApp: () => invoke("padmate:appBrowse"),
  explainProfile: (control, id) =>
    invoke("padmate:profileExplain", control, id),
  getLatency: (device) => invoke("padmate:latencyGet", device),
  resetLatency: (device) => invoke("padmate:latencyReset", device),
  getScriptDescription: (id) => invoke("padmate:scriptDescribe", id),
  getScriptRegistrations: (device) =>
    invoke("padmate:scriptRegistrations", device),
  listMacros: () => invoke("padmate:macrosList"),
  createMacro: (name) => invoke("padmate:macroCreate", name),
  importMacro: (options) => invoke("padmate:macroImport", options ?? null),
  renameMacro: (file, to) => invoke("padmate:macroRename", file, to),
  deleteMacro: (file) => invoke("padmate:macroDelete", file),
  detachScript: (id) => invoke("padmate:scriptDetach", id),
  editMacro: (file) => invoke("padmate:macroEdit", file),
  editScript: (id) => invoke("padmate:scriptEdit", id),
  captureStart: () => invoke("padmate:captureStart"),
  captureStop: (session) => invoke("padmate:captureStop", session),
  padCaptureStart: () => invoke("padmate:padCaptureStart"),
  padCaptureStop: () => invoke("padmate:padCaptureStop"),
  onCapture: (listener) =>
    listen<KeyboardCaptureEvent>("padmate:keyboardCapture", listener),
  getStartup: () => invoke("padmate:startupGet"),
  setStartup: (enabled) => invoke("padmate:startupSet", enabled),
  onClosing: (listener) => listen<void>("padmate:closing", () => listener()),
  readyToClose: () => invoke("padmate:readyToClose"),
  exportCheatSheet: (format, content) =>
    invoke("padmate:export", format, content),
  putConfig: (text, revision) =>
    ipcRenderer.invoke("padmate:configPut", text, revision),
  getScriptStatus: () => ipcRenderer.invoke("padmate:scriptStatus"),
  reloadScripts: () => ipcRenderer.invoke("padmate:scriptReload"),
  getScriptLogs: () => ipcRenderer.invoke("padmate:scriptLogs"),
  setSendToPadmate: (enabled) =>
    ipcRenderer.invoke("padmate:setOutput", enabled),
  onEvent: (listener) => {
    eventListeners.add(listener);
    return () => {
      eventListeners.delete(listener);
    };
  },
  reconnect: () => ipcRenderer.invoke("padmate:reconnect"),
  openStartFolder: () => invoke("padmate:startFolder"),
  retryStart: () => invoke("padmate:startRetry"),
  quit: () => invoke("padmate:quit"),
  markConfigRepairSeen: (backup) => invoke("padmate:configRepairSeen", backup),
  showConfigBackup: (backup) => invoke("padmate:configBackupShow", backup),
  setOverlayPrefs: (prefs) => invoke("padmate:overlayPrefs", prefs),
  getUpdateState: () => invoke("padmate:updateGet"),
  checkForUpdate: () => invoke("padmate:updateCheck"),
  downloadUpdate: () => invoke("padmate:updateDownload"),
  installUpdate: () => invoke("padmate:updateInstall"),
  dismissUpdate: (choice) => invoke("padmate:updateDismiss", choice),
  setAutoUpdate: (enabled) => invoke("padmate:updateAuto", enabled),
  openReleasePage: () => invoke("padmate:updateReleasePage"),
  onUpdateState: (listener) =>
    listen<UpdateState>("padmate:updateState", listener),
};

contextBridge.exposeInMainWorld("padmate", Object.freeze(api));
