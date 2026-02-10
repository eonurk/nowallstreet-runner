const { app, BrowserWindow, ipcMain, Tray, Menu, nativeImage, shell } = require('electron');
const path = require('path');
const os = require('os');
const fs = require('fs');
const { spawn } = require('child_process');

const RUNNER_HOST = process.env.RUNNER_HOST || '127.0.0.1';
const RUNNER_PORT = Number(process.env.RUNNER_PORT || 18100);
const RUNNER_BASE = `http://${RUNNER_HOST}:${RUNNER_PORT}`;

let mainWindow = null;
let tray = null;
let runnerProcess = null;

function platformFolder() {
  if (process.platform === 'darwin') return 'darwin-amd64';
  if (process.platform === 'linux') return 'linux-amd64';
  if (process.platform === 'win32') return 'windows-amd64';
  return '';
}

function bundledAgentdPath() {
  if (!app.isPackaged) {
    return '';
  }
  const folder = platformFolder();
  if (!folder) return '';
  const binName = process.platform === 'win32' ? 'agentd.exe' : 'agentd';
  const candidate = path.join(process.resourcesPath, 'agentd', folder, binName);
  return fs.existsSync(candidate) ? candidate : '';
}

function runnerScriptPath() {
  if (app.isPackaged) {
    return path.join(process.resourcesPath, 'runner', 'runner.mjs');
  }
  return path.resolve(__dirname, '../../runner/runner.mjs');
}

function runnerStateDir() {
  return path.join(os.homedir(), '.nowallstreet', 'runner');
}

function createTrayIcon() {
  const png =
    'iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAABeElEQVQ4T5WTP0vDQBDGf8+FC2lE0aG1NQm9QhAQf4AW2hA0iQKVoSEh0f4A2qS0CInuYOWxstcZCx7m7m8z2x9m2vV3T3n3vvd+7G7AZwH0hQx2n2f8CqM4x1V0m9p2qv9fSS9szYqlm4b2W7Q2P9TZ6K0iOQhW4mlQDy8yQ1fD4VxM1uU1xv+MAmW7xU6NYAAxkA1bM5s0qVfV7YBf3NfdG4DQwK6Jw4hW8Y1lyJBY8Vh4YQe7tQfA9Jj7S2r8b3m6A1lH2YAAQqQm2wC5Tn0z6N5S4g8tP8aK9v1S7v9Qkq3Rr8s4w0m3cQf9y2l9Yg2xJmWJ9lQ3Q+f9JTZfJ1jGcQmY8hU5owh5XyW8WgB4rQ5fJHj7s4D+8qX4mLeC6si1wQ3PjN1a+0xS2N0o0Y7l3Y3h0wV3Uq8K5vQf2kB6h3dYx2j5D9r7M8p4m9fM3J9m4rA5vNf6Cz1QnJ1bF8mQAAAABJRU5ErkJggg==';
  const image = nativeImage.createFromDataURL(`data:image/png;base64,${png}`);
  return image.resize({ width: 16, height: 16 });
}

async function runnerInfo() {
  try {
    const res = await fetch(`${RUNNER_BASE}/info`, { cache: 'no-store' });
    if (!res.ok) {
      return { ok: false, error: `runner info failed (${res.status})` };
    }
    return await res.json();
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : 'runner unreachable' };
  }
}

function startRunner() {
  if (runnerProcess && !runnerProcess.killed) return;
  const scriptPath = runnerScriptPath();
  if (!fs.existsSync(scriptPath)) {
    throw new Error(`Runner script not found: ${scriptPath}`);
  }

  const env = {
    ...process.env,
    RUNNER_HOST,
    RUNNER_PORT: String(RUNNER_PORT),
    LOCAL_BRIDGE_HOST: RUNNER_HOST,
    LOCAL_BRIDGE_PORT: String(RUNNER_PORT),
    ELECTRON_RUN_AS_NODE: '1'
  };
  const bundled = bundledAgentdPath();
  if (bundled) {
    env.RUNNER_AGENTD_PATH = bundled;
    env.RUNNER_AGENTD_SOURCE = bundled;
  }

  runnerProcess = spawn(process.execPath, [scriptPath, 'serve'], {
    env,
    stdio: 'ignore',
    windowsHide: true,
    detached: false
  });
  runnerProcess.unref();
  runnerProcess.on('exit', () => {
    runnerProcess = null;
  });
}

function stopRunner() {
  if (!runnerProcess || runnerProcess.killed) return;
  try {
    runnerProcess.kill('SIGTERM');
  } catch {
    // ignore
  }
  runnerProcess = null;
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 460,
    height: 360,
    minWidth: 420,
    minHeight: 320,
    title: 'NoWallStreet Runner',
    webPreferences: {
      contextIsolation: true,
      preload: path.join(__dirname, 'preload.js')
    }
  });
  mainWindow.loadFile(path.join(__dirname, 'index.html'));
  mainWindow.on('closed', () => {
    mainWindow = null;
  });
}

function createTray() {
  tray = new Tray(createTrayIcon());
  tray.setToolTip('NoWallStreet Runner');
  const refreshMenu = async () => {
    const info = await runnerInfo();
    const status = info && info.ok ? 'Online' : 'Offline';
    const menu = Menu.buildFromTemplate([
      {
        label: `Runner: ${status}`,
        enabled: false
      },
      {
        label: 'Open Runner',
        click: () => {
          if (!mainWindow) createWindow();
          if (mainWindow) mainWindow.show();
        }
      },
      {
        label: 'Open Runner Folder',
        click: async () => {
          await shell.openPath(runnerStateDir());
        }
      },
      {
        label: 'Open NoWallStreet',
        click: () => {
          shell.openExternal('https://nowall.co/profile');
        }
      },
      { type: 'separator' },
      {
        label: 'Quit',
        click: () => {
          app.quit();
        }
      }
    ]);
    tray.setContextMenu(menu);
  };
  tray.on('click', () => {
    if (!mainWindow) createWindow();
    if (mainWindow) {
      mainWindow.show();
      mainWindow.focus();
    }
  });
  refreshMenu();
  setInterval(refreshMenu, 5000);
}

ipcMain.handle('runner:info', async () => {
  return await runnerInfo();
});

ipcMain.handle('runner:open-folder', async () => {
  return shell.openPath(runnerStateDir());
});

ipcMain.handle('runner:open-profile', async () => {
  await shell.openExternal('https://nowall.co/profile');
  return true;
});

app.whenReady().then(() => {
  app.setName('NoWallStreet Runner');
  startRunner();
  createWindow();
  createTray();

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow();
    }
  });
});

app.on('before-quit', () => {
  stopRunner();
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') {
    // Keep running in tray on non-mac platforms only if app is packaged.
    if (!app.isPackaged) {
      app.quit();
    }
  }
});
