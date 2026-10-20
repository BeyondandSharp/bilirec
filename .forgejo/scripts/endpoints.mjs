// endpoints.mjs — the endpoint variables, and what the runner can do.
//
// Two jobs, both small:
//
//   1. CLASSIFY `APT_PROXY`, `NPM_PROXY` and `PIP_PROXY`. Their value may be a
//      real forward proxy or an internal MIRROR/registry/index, and the
//      difference matters because the value is handed to the Dockerfile as a
//      build argument (`APT_PROXY`, `APK_PROXY`, `NPM_REGISTRY`, `PIP_INDEX_URL`):
//      a mirror root is what a Dockerfile rewrites its sources with, while a
//      forward proxy reaches the build through BuildKit's own HTTP(S)_PROXY
//      forwarding and must NOT be passed as a repository. A repository answers
//      its index path (`2xx`); a proxy answers a plain HTTP error; no answer at
//      all keeps the operator value as a mirror.
//
//   2. PROBE the runner: is there a `docker` CLI, can it reach a daemon, and is
//      `buildx` available. All three are required — this Action drives
//      `docker buildx build` and has no classic-builder fallback.
//
// Nothing here installs anything: the job container must already provide the
// CLI and the plugin (mount them from the host, or use an image that has them).

import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';

/** Proxy variables the job honours, in the order they should win. */
export const PROXY_VARS = ['ALL_PROXY', 'HTTPS_PROXY', 'HTTP_PROXY', 'NO_PROXY', 'APT_PROXY'];

/**
 * The `*_PROXY` variable that names a manager's endpoint for *build-arg
 * classification*. apt, npm and pip are classified here: `APK_REPO` / `YUM_REPO`
 * are explicit repositories (never probed) and the apk build argument is derived
 * from the apt mirror. The job container's own package installs do not go through
 * this classification at all — the workflow bootstrap and `prepare` rewrite the
 * container's sources directly from `APK_REPO` / `APT_PROXY` / `YUM_REPO`.
 */
export const MANAGER_PROXY_VARS = {
  'apt-get': ['APT_PROXY'],
  npm: ['NPM_PROXY'],
  pip: ['PIP_PROXY'],
};

/** The Go module proxy, passed into the build as `--build-arg GOPROXY`. */
export function goProxyFrom(env = process.env) {
  return envValue(env, ['GOPROXY']);
}

/** The first non-empty of `names`, trimmed, without a trailing slash. */
export function envValue(env, names) {
  for (const name of names) {
    const value = env[name] ?? env[name.toLowerCase()];
    if (value && String(value).trim()) return String(value).trim().replace(/\/+$/, '');
  }
  return '';
}

/** `/etc/os-release` as an object ("" for a missing key), tolerant of quotes. */
export function parseOsRelease(text) {
  const out = {};
  for (const line of String(text || '').split('\n')) {
    const match = /^\s*([A-Z_]+)\s*=\s*(.*?)\s*$/.exec(line);
    if (!match) continue;
    out[match[1]] = match[2].replace(/^["']|["']$/g, '');
  }
  return out;
}

export function readOsRelease(path = '/etc/os-release') {
  try {
    return parseOsRelease(readFileSync(path, 'utf8'));
  } catch {
    return {};
  }
}

/**
 * The conventional paths of a Python package index. A value that already names
 * one of them is used as it is; a bare host is expanded to both candidates,
 * because a PyPI-compatible index lives under `/simple` (PEP 503) while the
 * caches built on proxpi (and a few mirrors) live under `/index`.
 */
const PIP_INDEX_PATH = /\/(?:simple|index)$/;

/**
 * Turn a configured endpoint into the concrete URL(s) the probe (and the
 * Dockerfile) should use. A URL that already names the repository path is passed
 * through; a bare mirror root gets the distro's own path from `/etc/os-release`
 * (`/debian` or `/ubuntu` for apt), pip tries the two conventional index paths,
 * and npm's registry is the value itself.
 */
export function expandEndpoint(manager, base, osRelease = {}) {
  const url = String(base || '').replace(/\/+$/, '');
  if (!url) return [];
  if (manager === 'apt-get') {
    const hasPath = /\/(?:debian|ubuntu|debian-security)$/.test(url);
    const path = hasPath ? '' : String(osRelease.ID || '') === 'ubuntu' ? '/ubuntu' : '/debian';
    return [`${url}${path}`];
  }
  if (manager === 'pip') {
    return PIP_INDEX_PATH.test(url) ? [url] : [`${url}/simple`, `${url}/index`];
  }
  return [url]; // npm: the registry is the value itself
}

/** The one file that proves a URL is a repository index for this manager. */
export function endpointProbeUrl(manager, endpoint, osRelease = {}) {
  if (manager === 'apt-get') {
    const suite = String(osRelease.VERSION_CODENAME || osRelease.VERSION_ID || '');
    return suite ? `${endpoint}/dists/${suite}/InRelease` : '';
  }
  if (manager === 'npm') return `${endpoint}/-/ping`;
  // Every Python package index must serve `pip` itself: `/simple/<project>/` is
  // PEP 503, `/index/<project>/` is the proxpi-style cache layout.
  if (manager === 'pip') return `${endpoint}/pip/`;
  return '';
}

/**
 * Probe every candidate endpoint of one configured value.
 *
 * Returns `{ answered, endpoint }`: `answered` means "this value is a repository"
 * and `endpoint` is the candidate that proved it (empty when no candidate could
 * be reached at all, so the caller keeps the operator value as a mirror). A bare
 * pip root has two candidates (`/simple`, `/index`) and the first one that
 * answers its index path decides, because the URL injected into the build must be
 * the one that actually serves the index — never the un-expanded root.
 *
 * A network error means "could not tell", and the value is kept as a mirror: that
 * is what the operator configured, and an unreachable endpoint fails either way.
 */
async function probeEndpointCandidates(
  base,
  manager,
  { fetchImpl = globalThis.fetch, osRelease = {}, timeoutMs = 8000 } = {},
) {
  const endpoints = expandEndpoint(manager, base, osRelease);
  let unreachable = false;
  for (const endpoint of endpoints) {
    const url = endpointProbeUrl(manager, endpoint, osRelease);
    if (!url) return { answered: true, endpoint }; // nothing to check against
    try {
      const response = await fetchImpl(url, { signal: AbortSignal.timeout(timeoutMs), redirect: 'follow' });
      if (response && response.ok) return { answered: true, endpoint };
    } catch {
      unreachable = true;
    }
  }
  return { answered: unreachable, endpoint: '' };
}

/**
 * Is this value a repository rather than a proxy?
 *
 * A repository answers its own index path; a proxy answers the same path with a
 * plain HTTP error (it expects absolute URIs or CONNECT). A network error means
 * "could not tell", and the value is kept as a mirror: that is what the operator
 * configured, and an unreachable endpoint fails either way.
 */
export async function probeEndpoint(base, manager, options = {}) {
  return (await probeEndpointCandidates(base, manager, options)).answered;
}

/**
 * Decide how one manager should use its configured endpoint.
 *
 * Returns `{ mirror, proxy, source }` where `source` is `'proxy-as-mirror'`
 * (inject it as a repository), `'proxy'` (leave it to BuildKit), or `''`
 * (nothing configured). `mirror` carries the endpoint that was actually
 * verified: for a bare pip root that is `<root>/simple` or `<root>/index`,
 * whichever answered.
 */
export async function resolveEndpoint(manager, env = process.env, { fetchImpl, osRelease, timeoutMs } = {}) {
  const release = osRelease || readOsRelease();
  const candidate = envValue(env, MANAGER_PROXY_VARS[manager] || []);
  if (!candidate) return { mirror: [], proxy: '', source: '' };
  const probe = await probeEndpointCandidates(candidate, manager, { fetchImpl, osRelease: release, timeoutMs });
  if (probe.answered) {
    const mirror = probe.endpoint ? [probe.endpoint] : expandEndpoint(manager, candidate, release).slice(0, 1);
    return { mirror, proxy: '', source: 'proxy-as-mirror' };
  }
  return { mirror: [], proxy: candidate, source: 'proxy' };
}

/** `buildx` is a docker subcommand, not a binary on PATH. */
export function hasBinary(binary, run = spawnSync) {
  if (binary === 'buildx') {
    return run('docker', ['buildx', 'version'], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).status === 0;
  }
  return run('sh', ['-c', `command -v ${binary}`], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).status === 0;
}

/**
 * What the runner can do. All three of `cli`, `daemon` and `buildx` are
 * required: without buildx there is no build path at all, and a container job
 * only reaches a daemon when the runner mounted the socket (or runs the job on
 * the host), which is the single most common setup mistake.
 *
 * `socketPath` / `socketPresent` sharpen the error: "the socket was never
 * mounted" and "the socket is there but the daemon refuses" need different fixes.
 */
export function probeDocker(env = process.env, run = spawnSync, { exists = existsSync } = {}) {
  if (!hasBinary('docker', run)) {
    return { cli: false, daemon: false, buildx: false, server: '', host: '', socketPath: '', socketPresent: null, error: 'docker 命令不存在' };
  }
  const version = run('docker', ['version', '--format', '{{.Server.Version}}'], {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  const server = version.status === 0 ? String(version.stdout || '').trim() : '';

  const host = String(env.DOCKER_HOST || '').trim();
  const socketPath = host.startsWith('unix://') ? host.slice('unix://'.length) : host === '' ? '/var/run/docker.sock' : '';
  let socketPresent = null;
  if (socketPath) {
    try {
      socketPresent = exists(socketPath);
    } catch {
      socketPresent = null;
    }
  }

  return {
    cli: true,
    daemon: version.status === 0,
    buildx: hasBinary('buildx', run),
    server,
    host,
    socketPath,
    socketPresent,
    error: version.status === 0 ? '' : String(version.stderr || 'docker version 失败').trim(),
  };
}
