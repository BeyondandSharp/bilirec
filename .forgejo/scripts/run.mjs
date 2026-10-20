// run.mjs — the single entry point the workflow invokes.
//
// Every workflow step is one line:
//     run: |
//       dir="${{ steps.locate.outputs.forgejo_dir }}"; node "$dir/scripts/run.mjs" <subcommand>
// so the YAML contains orchestration only, and all behaviour (including the
// shell-level work) lives in real, testable files.
//
// Subcommands:
//   locate-action      find the copied Action directory, emit forgejo_dir
//   verify-action      assert every shipped program is present, print capabilities
//   resolve            derive version/variants/images from the tag
//   preflight          credentials, Dockerfiles, tag collisions, docker daemon
//   login              docker login to ghcr.io and/or Docker Hub
//   meta               compute tags and labels per variant
//   build-push         one build per variant, then push
//   summary            digest table

import { spawn } from 'node:child_process';
import { existsSync, realpathSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { probeDocker } from './endpoints.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));

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

/** Programs that are executed as their own process (they own process.exit). */
const PROGRAMS = {
  'locate-action': 'locate-action.mjs',
  'plan-matrix': 'matrix.mjs',
  prepare: 'prepare.mjs',
  resolve: 'resolve.mjs',
  preflight: 'preflight.mjs',
  login: 'login.mjs',
  meta: 'meta.mjs',
  'build-push': 'build-push.mjs',
  publish: 'publish.mjs',
  summary: 'summary.mjs',
};

/** Everything the Action ships; verify-action insists on all of it. */
export const REQUIRED_SCRIPTS = [
  'endpoints.mjs',
  'locate-action.mjs',
  'run.mjs',
  'config.mjs',
  'matrix.mjs',
  'prepare.mjs',
  'resolve.mjs',
  'preflight.mjs',
  'login.mjs',
  'meta.mjs',
  'build-push.mjs',
  'publish.mjs',
  'summary.mjs',
];

export const SUBCOMMANDS = [
  'locate-action',
  'verify-action',
  'plan-matrix',
  'prepare',
  'resolve',
  'preflight',
  'login',
  'meta',
  'build-push',
  'publish',
  'summary',
];

/** The repository being built (GITHUB_WORKSPACE, falling back to cwd). */
export function workspaceDir(env = process.env) {
  return (env.GITHUB_WORKSPACE || process.cwd()).replace(/\/+$/, '');
}

/** Run a command in `cwd`, streaming output; resolves with its exit code. */
export function runCommand(command, args, { cwd, env } = {}) {
  return new Promise((resolve) => {
    process.stdout.write(`$ ${command} ${args.join(' ')}\n`);
    const child = spawn(command, args, {
      cwd,
      env: { ...process.env, ...env },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    child.stdout.setEncoding('utf8');
    child.stderr.setEncoding('utf8');
    child.stdout.on('data', (chunk) => process.stdout.write(chunk));
    child.stderr.on('data', (chunk) => process.stderr.write(chunk));
    child.on('error', (error) => {
      process.stderr.write(`无法启动 ${command}：${error.message}\n`);
      resolve(127);
    });
    child.on('close', (code) => resolve(code ?? 1));
  });
}

async function runSubprocessProgram(name, env) {
  const program = PROGRAMS[name];
  const path = join(HERE, program);
  if (!existsSync(path)) {
    process.stderr.write(`缺少程序：${path}\n`);
    return 1;
  }
  return runCommand(process.execPath, [path], { cwd: workspaceDir(env), env });
}

async function verifyAction(env) {
  const dir = (env.GITHUB_ACTION_PATH || HERE.replace(/\/scripts$/, '')).replace(/\/+$/, '');
  const missing = REQUIRED_SCRIPTS.filter((name) => !existsSync(join(dir, 'scripts', name)));
  if (missing.length > 0) {
    for (const name of missing) process.stderr.write(`缺少 ${dir}/scripts/${name}\n`);
    process.stderr.write(
      `Action 目录不完整：请整目录复制 docker-build-push/（workflows/ 与 scripts/ 都要）后改名为 .forgejo\n`,
    );
    return 1;
  }
  process.stdout.write(`Action 脚本齐全：${dir}/scripts（${REQUIRED_SCRIPTS.length} 个文件）\n`);

  const docker = probeDocker(env);
  process.stdout.write(
    `运行环境：docker=${docker.cli ? `ok${docker.server ? `(${docker.server})` : ''}` : '缺失'} ` +
      `daemon=${docker.daemon ? 'ok' : '不可达'} buildx=${docker.buildx ? 'ok' : '缺失'}\n`,
  );
  if (!docker.daemon) {
    process.stdout.write(
      '提示：daemon 暂不可达。默认接法下 job 容器就是 docker:dind，' +
        'prepare 步骤会在容器内启动 dockerd（runner 需要 container.privileged: true）。\n',
    );
  }
  if (!docker.buildx) {
    process.stdout.write('提示：本 Action 只用 `docker buildx build`，缺 buildx 时 preflight 会失败；\n');
    process.stdout.write('      默认 docker:dind / docker:cli 自带 buildx，prepare 也会尝试补装。\n');
  }
  return 0;
}

export async function dispatch(name, env = process.env) {
  if (!SUBCOMMANDS.includes(name)) {
    process.stderr.write(`未知子命令：${name || '<empty>'}（可选：${SUBCOMMANDS.join(', ')}）\n`);
    return 2;
  }
  switch (name) {
    case 'verify-action':
      return verifyAction(env);
    default:
      return runSubprocessProgram(name, env);
  }
}

if (IS_DIRECT) {
  const name = process.argv[2] || '';
  dispatch(name).then(
    (code) => process.exit(code),
    (error) => {
      process.stderr.write(`执行 ${name} 失败：${error.message}\n`);
      process.exit(1);
    },
  );
}
