function setStatus(ok, text) {
  const el = document.getElementById('runnerStatus');
  if (!el) return;
  el.textContent = text;
  el.classList.remove('ok', 'warn');
  el.classList.add(ok ? 'ok' : 'warn');
}

function setText(id, value) {
  const el = document.getElementById(id);
  if (!el) return;
  el.textContent = value;
}

async function refresh() {
  try {
    const info = await window.runnerAPI.info();
    if (!info || !info.ok) {
      setStatus(false, `Offline: ${info?.error || 'runner unreachable'}`);
      setText('agentdStatus', 'Not available');
      return;
    }
    setStatus(true, 'Runner Online');
    setText('stateDir', info.state_dir || '~/.nowallstreet/runner');
    if (info.agentd_installed) {
      setText('agentdStatus', `Ready (${info.agentd_path || 'agentd'})`);
    } else {
      setText('agentdStatus', 'Missing agentd (run install step)');
    }
  } catch (err) {
    setStatus(false, `Offline: ${err instanceof Error ? err.message : 'runner unreachable'}`);
    setText('agentdStatus', 'Not available');
  }
}

document.getElementById('refreshBtn')?.addEventListener('click', () => {
  refresh();
});

document.getElementById('openFolderBtn')?.addEventListener('click', () => {
  window.runnerAPI.openFolder();
});

document.getElementById('openProfileBtn')?.addEventListener('click', () => {
  window.runnerAPI.openProfile();
});

refresh();
setInterval(refresh, 4000);
