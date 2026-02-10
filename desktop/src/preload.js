const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('runnerAPI', {
  info: () => ipcRenderer.invoke('runner:info'),
  openFolder: () => ipcRenderer.invoke('runner:open-folder'),
  openProfile: () => ipcRenderer.invoke('runner:open-profile')
});
