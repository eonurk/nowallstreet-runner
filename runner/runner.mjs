#!/usr/bin/env node

import http from 'http';
import fs from 'fs';
import os from 'os';
import path from 'path';
import { spawn, spawnSync } from 'child_process';
import { URL, fileURLToPath } from 'url';

const HOST = process.env.LOCAL_BRIDGE_HOST || process.env.RUNNER_HOST || '127.0.0.1';
const PORT = Number(process.env.LOCAL_BRIDGE_PORT || process.env.RUNNER_PORT || 18100);
const stateDir = path.join(os.homedir(), '.nowallstreet', 'runner');
const defaultOrigins =
  'http://localhost:3000,http://127.0.0.1:3000,http://0.0.0.0:3000,https://nowall.co,https://www.nowall.co';
const allowedOrigins = new Set(
  String(process.env.LOCAL_BRIDGE_ALLOWED_ORIGINS || process.env.RUNNER_ALLOWED_ORIGINS || defaultOrigins)
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean),
);

const bundledAgentd = path.join(os.homedir(), '.nowallstreet', 'runner', 'bin', 'agentd');
const currentFile = fileURLToPath(import.meta.url);
const runnerDir = path.dirname(currentFile);
const repoRoot = path.resolve(runnerDir, '..');

fs.mkdirSync(stateDir, { recursive: true });

function sanitizeAgentID(agentID) {
  return String(agentID || '').trim().replace(/[^a-zA-Z0-9_-]/g, '');
}

function pidFile(agentID) {
  return path.join(stateDir, `${sanitizeAgentID(agentID)}.pid`);
}

function logFile(agentID) {
  return path.join(stateDir, `${sanitizeAgentID(agentID)}.log`);
}

function processAlive(pid) {
  if (!Number.isInteger(pid) || pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

function readPID(agentID) {
  try {
    const raw = fs.readFileSync(pidFile(agentID), 'utf8').trim();
    const parsed = Number(raw);
    if (!Number.isInteger(parsed) || parsed <= 0) return 0;
    return parsed;
  } catch {
    return 0;
  }
}

function statusFor(agentID) {
  const safeAgentID = sanitizeAgentID(agentID);
  const pid = readPID(safeAgentID);
  const running = processAlive(pid);
  if (!running && pid > 0) {
    try {
      fs.rmSync(pidFile(safeAgentID), { force: true });
    } catch {
      // ignore
    }
  }
  return {
    ok: true,
    agent_id: safeAgentID,
    running,
    pid: running ? pid : 0,
    log_file: logFile(safeAgentID),
  };
}

function writeJSON(res, code, payload) {
  res.statusCode = code;
  res.setHeader('Content-Type', 'application/json');
  res.end(JSON.stringify(payload));
}

function applyCORS(req, res) {
  const origin = String(req.headers.origin || '').trim();
  if (!origin) return true;
  if (!allowedOrigins.has(origin)) return false;
  res.setHeader('Access-Control-Allow-Origin', origin);
  res.setHeader('Access-Control-Allow-Methods', 'GET,POST,OPTIONS');
  res.setHeader('Access-Control-Allow-Headers', 'Content-Type, Authorization');
  res.setHeader('Access-Control-Allow-Private-Network', 'true');
  res.setHeader('Vary', 'Origin, Access-Control-Request-Private-Network');
  return true;
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    let raw = '';
    req.on('data', (chunk) => {
      raw += chunk;
      if (raw.length > 1024 * 1024) {
        reject(new Error('payload too large'));
      }
    });
    req.on('end', () => {
      if (!raw.trim()) {
        resolve({});
        return;
      }
      try {
        resolve(JSON.parse(raw));
      } catch {
        reject(new Error('invalid json'));
      }
    });
    req.on('error', reject);
  });
}

function resolveHomeFromCommand(command) {
  const match = String(command || '').match(/(?:^|\s)HOME=([^\s]+)/);
  if (!match) return '';
  let raw = String(match[1] || '').trim();
  if (!raw) return '';
  if (raw.startsWith('~')) {
    raw = path.join(os.homedir(), raw.slice(1));
  } else if (raw.startsWith('$HOME/')) {
    raw = path.join(os.homedir(), raw.slice('$HOME/'.length));
  } else if (raw === '$HOME') {
    raw = os.homedir();
  }
  return raw;
}

function resolveAgentdPathFromCommand(command) {
  const match = String(command || '').match(/(?:^|\s)([^\s]*agentd)\s+run(?:\s|$)/);
  if (!match) return '/tmp/agentd';
  return String(match[1] || '/tmp/agentd').trim();
}

function isExecutable(filePath) {
  const target = String(filePath || '').trim();
  if (!target) return false;
  try {
    fs.accessSync(target, fs.constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

function findInPath(binary) {
  const name = String(binary || '').trim();
  if (!name) return '';
  const pathExt = process.platform === 'win32'
    ? String(process.env.PATHEXT || '.EXE;.CMD;.BAT')
        .split(';')
        .map((item) => item.trim().toLowerCase())
        .filter(Boolean)
    : [''];
  const paths = String(process.env.PATH || '')
    .split(path.delimiter)
    .map((item) => item.trim())
    .filter(Boolean);
  for (const base of paths) {
    for (const ext of pathExt) {
      const candidate = path.join(base, process.platform === 'win32' && ext ? `${name}${ext}` : name);
      if (isExecutable(candidate)) {
        return candidate;
      }
    }
  }
  return '';
}

function stripOuterQuotes(raw) {
  const text = String(raw || '').trim();
  if (!text) return '';
  if ((text.startsWith('"') && text.endsWith('"')) || (text.startsWith("'") && text.endsWith("'"))) {
    return text.slice(1, -1);
  }
  return text;
}

function expandEnvValue(raw) {
  let value = stripOuterQuotes(raw);
  if (!value) return value;
  if (value === '$HOME') return os.homedir();
  if (value.startsWith('$HOME/')) return path.join(os.homedir(), value.slice('$HOME/'.length));
  if (value.startsWith('~/')) return path.join(os.homedir(), value.slice(2));
  value = value.replace(/\$HOME/g, os.homedir());
  return value;
}

function tokenizeCommand(raw) {
  const input = String(raw || '').trim();
  if (!input) return [];
  const tokens = [];
  const regex = /"([^"\\]|\\.)*"|'([^'\\]|\\.)*'|\S+/g;
  let match;
  while ((match = regex.exec(input)) != null) {
    tokens.push(match[0]);
  }
  return tokens;
}

function parseLaunchCommand(command, agentdPath) {
  const tokens = tokenizeCommand(command);
  if (tokens.length === 0) {
    throw new Error('run command is empty');
  }
  const envVars = {};
  let idx = 0;
  while (idx < tokens.length) {
    const token = String(tokens[idx] || '');
    if (!/^[A-Za-z_][A-Za-z0-9_]*=/.test(token)) break;
    const eq = token.indexOf('=');
    const key = token.slice(0, eq).trim();
    const value = expandEnvValue(token.slice(eq + 1));
    if (key) envVars[key] = value;
    idx += 1;
  }
  if (idx >= tokens.length) {
    throw new Error('run command missing executable');
  }
  const executable = String(agentdPath || stripOuterQuotes(tokens[idx])).trim();
  const args = tokens.slice(idx + 1).map((item) => stripOuterQuotes(item)).filter(Boolean);
  if (!executable) {
    throw new Error('run command executable missing');
  }
  return { executable, args, envVars };
}

function detectAgentdBinary(preferredFromCommand = '') {
  const candidates = [
    String(process.env.RUNNER_AGENTD_PATH || '').trim(),
    String(preferredFromCommand || '').trim(),
    bundledAgentd,
    '/tmp/agentd',
    findInPath('agentd'),
  ].filter(Boolean);
  for (const candidate of candidates) {
    if (isExecutable(candidate)) {
      return candidate;
    }
  }
  return '';
}

function ensureAgentInitialized(home, agentd, outPath) {
  if (!home) return;
  const cfgPath = path.join(home, '.agentmarket', 'config.yaml');
  if (fs.existsSync(cfgPath)) return;

  fs.mkdirSync(home, { recursive: true });
  if (!agentd) {
    throw new Error('agentd binary not found. Run: node runner/runner.mjs install-agentd');
  }
  const fd = fs.openSync(outPath, 'a');
  try {
    const init = spawnSync(agentd, ['init'], {
      env: { ...process.env, HOME: home },
      stdio: ['ignore', fd, fd],
    });
    if (init.error) {
      throw init.error;
    }
    if (init.status !== 0) {
      throw new Error(`agent init failed (${init.status})`);
    }
  } finally {
    fs.closeSync(fd);
  }
}

function sanitizeExtraEnv(raw) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {};
  const allowed = new Set([
    'LLM_API_KEY',
    'OPENAI_API_KEY',
    'OPENROUTER_API_KEY',
    'GROQ_API_KEY',
    'TOGETHER_API_KEY',
    'ANTHROPIC_API_KEY',
  ]);
  const out = {};
  for (const [key, value] of Object.entries(raw)) {
    const cleanKey = String(key || '').trim().toUpperCase();
    if (!allowed.has(cleanKey)) continue;
    const cleanVal = String(value ?? '').trim();
    if (!cleanVal || cleanVal.length > 8192) continue;
    out[cleanKey] = cleanVal;
  }
  return out;
}

async function resolveRunCommand(agentID, launchToken, resolveURL) {
  const safeAgentID = sanitizeAgentID(agentID);
  const token = String(launchToken || '').trim();
  const endpoint = String(resolveURL || '').trim();
  if (!safeAgentID) {
    throw new Error('agent_id is required');
  }
  if (!token) {
    throw new Error('launch_token is required');
  }
  if (!endpoint) {
    throw new Error('resolve_url is required');
  }
  let parsed;
  try {
    parsed = new URL(endpoint);
  } catch {
    throw new Error('resolve_url is invalid');
  }
  if (!['http:', 'https:'].includes(parsed.protocol)) {
    throw new Error('resolve_url protocol is invalid');
  }

  const res = await fetch(parsed.toString(), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ agent_id: safeAgentID, launch_token: token }),
  });
  const text = await res.text();
  let data = {};
  try {
    data = text ? JSON.parse(text) : {};
  } catch {
    data = {};
  }
  if (!res.ok) {
    const message = data && typeof data === 'object' && 'error' in data ? String(data.error || '') : '';
    throw new Error(message || `failed to resolve launch config (${res.status})`);
  }
  if (!data || typeof data !== 'object') {
    throw new Error('invalid resolver payload');
  }
  if (String(data.agent_id || '').trim() !== safeAgentID) {
    throw new Error('resolved config agent mismatch');
  }
  const command = String(data.run_command || '').trim();
  if (!command) {
    throw new Error('resolved run command missing');
  }
  return command;
}

async function startAgent(agentID, runCommand, extraEnvRaw, launchToken, resolveURL) {
  const safeAgentID = sanitizeAgentID(agentID);
  if (!safeAgentID) {
    throw new Error('agent_id is required');
  }

  let command = String(runCommand || '').trim();
  if (!command) {
    command = await resolveRunCommand(safeAgentID, launchToken, resolveURL);
  }
  if (!command) {
    throw new Error('run command is required');
  }
  if (!command.includes(`--agent-id ${safeAgentID}`)) {
    throw new Error('run command does not match agent_id');
  }

  const current = statusFor(safeAgentID);
  if (current.running) return current;

  const preferredBinary = resolveAgentdPathFromCommand(command);
  const localBinary = detectAgentdBinary(preferredBinary);
  if (!localBinary) {
    throw new Error('agentd binary not found. Install it with: node runner/runner.mjs install-agentd');
  }

  const outPath = logFile(safeAgentID);
  const extraEnv = sanitizeExtraEnv(extraEnvRaw);
  const parsed = parseLaunchCommand(command, localBinary);
  const home = String(parsed.envVars.HOME || resolveHomeFromCommand(command) || '').trim();
  ensureAgentInitialized(home, localBinary, outPath);
  const fd = fs.openSync(outPath, 'a');
  const child = spawn(parsed.executable, parsed.args, {
    detached: true,
    stdio: ['ignore', fd, fd],
    windowsHide: true,
    env: { ...process.env, ...parsed.envVars, ...extraEnv },
  });
  child.unref();
  fs.closeSync(fd);
  fs.writeFileSync(pidFile(safeAgentID), `${child.pid}\n`, 'utf8');
  return statusFor(safeAgentID);
}

function stopAgent(agentID) {
  const safeAgentID = sanitizeAgentID(agentID);
  if (!safeAgentID) {
    throw new Error('agent_id is required');
  }
  const pid = readPID(safeAgentID);
  if (pid > 0 && processAlive(pid)) {
    try {
      process.kill(pid, 'SIGTERM');
    } catch {
      // ignore
    }
    setTimeout(() => {
      if (processAlive(pid)) {
        try {
          process.kill(pid, 'SIGKILL');
        } catch {
          // ignore
        }
      }
    }, 700);
  }
  try {
    fs.rmSync(pidFile(safeAgentID), { force: true });
  } catch {
    // ignore
  }
  return statusFor(safeAgentID);
}

function runnerInfo() {
  const detected = detectAgentdBinary('');
  return {
    ok: true,
    service: 'nowallstreet-runner',
    version: '1',
    host: HOST,
    port: PORT,
    state_dir: stateDir,
    agentd_path: detected || '',
    agentd_installed: Boolean(detected),
    allowed_origins: Array.from(allowedOrigins),
  };
}

function installAgentd() {
  const distFolder = path.join(repoRoot, 'agent', 'dist');
  const platformFolder =
    process.platform === 'darwin'
      ? 'darwin-amd64'
      : process.platform === 'linux'
        ? 'linux-amd64'
        : process.platform === 'win32'
          ? 'windows-amd64'
          : '';
  const platformDistBinary =
    platformFolder === ''
      ? ''
      : path.join(distFolder, platformFolder, process.platform === 'win32' ? 'agentd.exe' : 'agentd');
  const sourceCandidates = [
    String(process.env.RUNNER_AGENTD_SOURCE || '').trim(),
    platformDistBinary,
    process.platform === 'win32' ? 'C:\\tmp\\agentd.exe' : '',
    '/tmp/agentd',
    findInPath('agentd'),
  ].filter(Boolean);
  const source = sourceCandidates.find((item) => isExecutable(item)) || '';
  if (!source) {
    throw new Error('No agentd binary found to install. Build agentd first or set RUNNER_AGENTD_SOURCE.');
  }
  const destDir = path.dirname(bundledAgentd);
  fs.mkdirSync(destDir, { recursive: true });
  fs.copyFileSync(source, bundledAgentd);
  fs.chmodSync(bundledAgentd, 0o755);
  return {
    ok: true,
    source,
    installed_to: bundledAgentd,
  };
}

function serve() {
  const server = http.createServer(async (req, res) => {
    if (!applyCORS(req, res)) {
      writeJSON(res, 403, { error: 'origin not allowed' });
      return;
    }
    if (req.method === 'OPTIONS') {
      res.statusCode = 204;
      res.end();
      return;
    }

    let parsed;
    try {
      parsed = new URL(req.url || '/', `http://${HOST}:${PORT}`);
    } catch {
      writeJSON(res, 400, { error: 'invalid request url' });
      return;
    }

    if (req.method === 'GET' && parsed.pathname === '/health') {
      writeJSON(res, 200, runnerInfo());
      return;
    }

    if (req.method === 'GET' && parsed.pathname === '/info') {
      writeJSON(res, 200, runnerInfo());
      return;
    }

    if (req.method === 'GET' && parsed.pathname === '/status') {
      const agentID = sanitizeAgentID(parsed.searchParams.get('agent_id') || '');
      if (!agentID) {
        writeJSON(res, 400, { error: 'agent_id is required' });
        return;
      }
      writeJSON(res, 200, statusFor(agentID));
      return;
    }

    if (req.method === 'POST' && parsed.pathname === '/start') {
      try {
        const body = await readBody(req);
        const status = await startAgent(body.agent_id, body.run_command, body.env, body.launch_token, body.resolve_url);
        writeJSON(res, 200, status);
        return;
      } catch (err) {
        writeJSON(res, 400, { error: err instanceof Error ? err.message : 'start failed' });
        return;
      }
    }

    if (req.method === 'POST' && parsed.pathname === '/stop') {
      try {
        const body = await readBody(req);
        const status = stopAgent(body.agent_id);
        writeJSON(res, 200, status);
        return;
      } catch (err) {
        writeJSON(res, 400, { error: err instanceof Error ? err.message : 'stop failed' });
        return;
      }
    }

    if (req.method === 'POST' && parsed.pathname === '/install-agentd') {
      try {
        const result = installAgentd();
        writeJSON(res, 200, result);
        return;
      } catch (err) {
        writeJSON(res, 400, { error: err instanceof Error ? err.message : 'install failed' });
        return;
      }
    }

    writeJSON(res, 404, { error: 'not found' });
  });

  server.listen(PORT, HOST, () => {
    const origins = Array.from(allowedOrigins).join(', ');
    console.log(`[runner] listening on http://${HOST}:${PORT}`);
    console.log(`[runner] allowed origins: ${origins}`);
    const info = runnerInfo();
    console.log(`[runner] agentd: ${info.agentd_installed ? info.agentd_path : 'not installed'}`);
  });
}

function doctor() {
  const info = runnerInfo();
  console.log(`[runner] host: ${info.host}`);
  console.log(`[runner] port: ${info.port}`);
  console.log(`[runner] state: ${info.state_dir}`);
  console.log(`[runner] agentd: ${info.agentd_installed ? info.agentd_path : 'missing'}`);
  if (!info.agentd_installed) {
    console.log('[runner] fix: node runner/runner.mjs install-agentd');
    process.exitCode = 1;
  }
}

function usage() {
  console.log('NoWallStreet Runner');
  console.log('Usage: node runner/runner.mjs <serve|doctor|install-agentd>');
}

const command = String(process.argv[2] || 'serve').trim().toLowerCase();
switch (command) {
  case 'serve':
    serve();
    break;
  case 'doctor':
    doctor();
    break;
  case 'install-agentd':
    try {
      const result = installAgentd();
      console.log(`[runner] installed agentd from ${result.source} to ${result.installed_to}`);
    } catch (err) {
      console.error(`[runner] ${err instanceof Error ? err.message : 'install failed'}`);
      process.exitCode = 1;
    }
    break;
  case 'help':
  case '--help':
  case '-h':
    usage();
    break;
  default:
    usage();
    process.exitCode = 1;
}
