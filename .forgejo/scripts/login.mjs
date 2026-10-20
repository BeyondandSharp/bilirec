// login.mjs — the Forgejo port of docker/login-action, limited to ghcr.io and
// Docker Hub.
//
// Two rules from the official action are kept exactly:
//   * credentials live in the environment (the workflow maps `secrets.X ||
//     vars.X` into `env:`), never in a command line — the password reaches
//     `docker login` through stdin, so it cannot show up in the log and cannot
//     leak through /proc;
//   * a registry is only logged in to when it is actually going to be used.
//
// The token values are masked with `::add-mask::` before the first use, which
// is belt-and-braces: preflight has already refused to run without them.

import { readFileSync, realpathSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { statePath } from './resolve.mjs';

// Run directly (argv[1] is this file) rather than imported by a test.
// Compare real paths: /tmp is a symlink on some hosts, and path.resolve
// would then disagree with import.meta.url.
export const IS_DIRECT = (() => {
  if (!process.argv[1] || !process.argv[1].endsWith('.mjs')) return false;
  try {
    return realpathSync(process.argv[1]) === realpathSync(fileURLToPath(import.meta.url));
  } catch {
    return false;
  }
})();

/** `docker login <registry> -u <user> --password-stdin` — never a token in argv. */
export function loginArgv({ registry, user }) {
  return ['login', registry, '-u', user, '--password-stdin'];
}

/** The registries this run must log in to, with the variable holding each token. */
export function planLogins(state) {
  return state.registries
    .filter((entry) => entry.enabled)
    .map((entry) => ({ id: entry.id, registry: entry.registry, user: entry.user, tokenVar: entry.tokenVar }));
}

/** Ask the runner to mask every credential it is about to use. */
export function maskCommands(tokens) {
  return tokens.filter((token) => token).map((token) => `::add-mask::${token}`);
}

/** One `docker login`, with the token fed through stdin. */
export function login({ entry, token, env = process.env, run = spawnSync }) {
  if (!token) throw new Error(`缺少凭据：${entry.tokenVar}`);
  const result = run('docker', loginArgv(entry), {
    input: `${token}\n`,
    encoding: 'utf8',
    stdio: ['pipe', 'pipe', 'pipe'],
    env,
  });
  const status = result.status === 0 ? 0 : result.status ?? 1;
  return {
    ok: status === 0,
    status,
    stdout: String(result.stdout || '').trim(),
    stderr: String(result.stderr || result.error?.message || '').trim(),
  };
}

const HINTS = {
  ghcr: 'ghcr.io 需要 GitHub PAT（classic 勾选 write:packages，或 fine-grained 勾选 Packages: write）；镜像名用 GHCR_IMAGE（用户名即命名空间）',
  dockerhub: 'Docker Hub 需要 access token（不是账号密码）；用户名填 DOCKERHUB_USERNAME',
};

async function main() {
  const state = JSON.parse(readFileSync(statePath(process.env), 'utf8'));
  if (state.dryRun) {
    process.stdout.write('dry run：跳过 docker login\n');
    return;
  }

  const plan = planLogins(state);
  if (plan.length === 0) {
    process.stderr.write('没有启用的 registry，无法推送\n');
    process.exit(1);
  }

  const tokens = plan.map((entry) => process.env[entry.tokenVar] || '');
  for (const line of maskCommands(tokens)) process.stdout.write(`${line}\n`);

  for (const entry of plan) {
    const token = process.env[entry.tokenVar] || '';
    process.stdout.write(`$ docker login ${entry.registry} -u ${entry.user} --password-stdin\n`);
    const result = login({ entry, token });
    if (!result.ok) {
      process.stderr.write(`登录 ${entry.registry} 失败：${result.stderr || `退出码 ${result.status}`}\n`);
      process.stderr.write(`提示：${HINTS[entry.id] || ''}\n`);
      process.exit(1);
    }
    process.stdout.write(`已登录 ${entry.registry}（${entry.user}）\n`);
  }
}

if (IS_DIRECT) {
  try {
    await main();
  } catch (error) {
    process.stderr.write(`登录失败：${error.message}\n`);
    process.exit(1);
  }
}
