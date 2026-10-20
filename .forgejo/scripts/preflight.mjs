// preflight.mjs — refuse to start a publish that cannot succeed.
//
// Everything that can be checked without touching a registry is checked here,
// before `docker login` runs: credentials, image names, the variant table, the
// Dockerfiles that must exist, tag collisions between variants, and whether the
// runner can actually reach a Docker daemon.
//
// Every finding is a hard error with an actionable message: there are no
// degradations left, because build-push.mjs only knows how to drive buildx.

import { existsSync, readFileSync, realpathSync } from 'node:fs';
import { isAbsolute, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { probeDocker } from './endpoints.mjs';
import { platformsFor, readOptional } from './config.mjs';
import { computeBuilds } from './meta.mjs';
import { BINFMT_ROOT, archesOf, binfmtHandler, hostArch, normalizeArch } from './prepare.mjs';
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

export const DOCKER_HELP = [
  'runner 侧检查清单：',
  '  1) 默认接法（job 容器 = docker:dind）：在 runner 配置里打开 privileged，然后**重启 runner**：',
  '       container:',
  '         privileged: true',
  '     dind 需要 privileged，而 workflow 里的 `options: --privileged` 会被 runner 忽略（权限只能由 runner 侧给）。',
  '  2) daemon 由 runner 提供：设置 container.docker_host: automount，或用仓库变量 DOCKER_HOST 指向可达的 daemon；',
  '     这两种接法都不需要 volume 挂载，也不需要 valid_volumes。',
  '  3) job 容器里必须有 docker CLI 与 buildx：默认 docker:dind / docker:cli 自带；换镜像时 prepare 会尝试补装，',
  '     失败时打印可用的安装方式。',
  '  4) 只用 `docker buildx build`（没有经典构建回退）：缺 CLI / daemon / buildx 都会在构建前失败。',
].join('\n');

/** Foreign architectures this run needs that the kernel has no QEMU handler for. */
export function missingBinfmt(platforms = [], { arch = '', exists = existsSync, root = BINFMT_ROOT } = {}) {
  const host = normalizeArch(arch) || normalizeArch(hostArch());
  return archesOf(platforms).filter((candidate) => candidate !== host && !exists(join(root, binfmtHandler(candidate))));
}

/** A sharper reason than "docker version failed": no daemon at all vs one refusing. */
export function dockerUnreachableReason(docker) {
  if (docker.socketPresent === false) {
    return (
      `容器内看不到 ${docker.socketPath}：说明 dockerd 没有起来（prepare 会尝试启动它），` +
      '或 daemon 不在默认路径（用 DOCKER_HOST / container.docker_host 指定）'
    );
  }
  if (docker.socketPresent === true) {
    return `${docker.socketPath} 存在但守护进程不可达：检查 dockerd 日志与权限（清单第 1 条）`;
  }
  return `DOCKER_HOST=${docker.host || '<unset>'} 不可达`;
}

/** Which files a build plan needs. `target` selects a stage inside the file. */
export function missingDockerfiles(builds, context, exists = existsSync) {
  const missing = [];
  for (const build of builds) {
    const full = isAbsolute(build.dockerfile) ? build.dockerfile : join(context, build.dockerfile);
    if (!exists(full)) missing.push(`${build.variant}: ${full}`);
  }
  return missing;
}

/**
 * All preflight findings. Pure except for the injected probes, so the tests can
 * drive every branch without a daemon.
 */
export function preflightChecks({
  state,
  env = process.env,
  docker = probeDocker(env),
  exists = existsSync,
  arch = '',
  now = new Date(),
}) {
  const errors = [];
  const warnings = [];

  const enabled = state.registries.filter((entry) => entry.enabled);
  const halfConfigured = [];
  if (readOptional(env.DOCKERHUB_TOKEN) !== '' && readOptional(env.DOCKERHUB_USERNAME) === '') halfConfigured.push('DOCKERHUB_TOKEN');
  if (readOptional(env.GHCR_TOKEN) !== '' && (enabled.find((entry) => entry.id === 'ghcr')?.user || '') === '') halfConfigured.push('GHCR_TOKEN');
  if (halfConfigured.length > 0) {
    errors.push(
      `${halfConfigured.join(' / ')} 配置不完整：Docker Hub 需要 DOCKERHUB_USERNAME + DOCKERHUB_TOKEN，` +
        'ghcr.io 需要 GHCR_IMAGE（或仓库 owner）与 GHCR_TOKEN',
    );
  }
  if (enabled.length === 0 && !state.dryRun) {
    errors.push(
      '没有可用的 registry 凭据：请至少配置 GHCR_TOKEN，或 DOCKERHUB_USERNAME + DOCKERHUB_TOKEN' +
        '（这两个是 Secrets，也可以放 Variables）',
    );
  }
  if (enabled.length === 0 && state.dryRun) {
    warnings.push('dry run 且没有任何 registry 凭据：只验证构建，不会推送');
  }
  if (state.images.length === 0 && !state.dryRun) {
    errors.push('没有任何镜像要发布（检查 registry 凭据与 DOCKER_META_IMAGES）');
  }

  let builds = [];
  try {
    builds = computeBuilds({
      state,
      // computeBuilds() turns an empty (unconfigured) value into the default.
      tagsRaw: env.DOCKER_META_TAGS,
      flavorRaw: env.DOCKER_META_FLAVOR,
      labelsRaw: env.DOCKER_META_LABELS,
      now,
    });
  } catch (error) {
    errors.push(error.message);
  }

  // Publication only talks to the registries (`buildx imagetools create`), so it
  // needs no daemon and no QEMU; a build needs both. The workflow marks the
  // publish job with DOCKER_STEP=publish.
  const publishOnly = readOptional(env.DOCKER_STEP) === 'publish';

  if (builds.length > 0) {
    const missing = missingDockerfiles(builds, state.options.context, exists);
    if (missing.length > 0) {
      errors.push(`找不到变体的 Dockerfile：${missing.join('、')}（构建上下文 ${state.options.context}）`);
    }
    // A multi-architecture variant needs QEMU handlers on the host kernel.
    // prepare registers them, but a host that already has them is normal too, so
    // this stays a warning: the build itself would fail with "exec format error".
    const requested = [...new Set(builds.flatMap((build) => platformsFor(build, state.options)))];
    const foreign = publishOnly ? [] : missingBinfmt(requested, { arch, exists });
    if (foreign.length > 0) {
      warnings.push(
        `本次构建需要 ${foreign.join('、')} 的 QEMU 处理器，但 /proc/sys/fs/binfmt_misc 里没有对应条目：` +
          '跨架构的 RUN 步骤可能报 "exec format error"（prepare 会尝试注册；宿主已有 binfmt 时请忽略）',
      );
    }
  }

  if (!docker.cli) {
    errors.push(`runner 里没有 docker 命令：${docker.error}\n${DOCKER_HELP}`);
  } else if (!publishOnly && !docker.daemon) {
    errors.push(
      `连不上 Docker 守护进程：${dockerUnreachableReason(docker)}\n` +
        `docker version 的原始报错：${docker.error || 'unknown'}\n${DOCKER_HELP}`,
    );
  } else if (!docker.buildx) {
    errors.push(
      'runner 里没有 docker buildx：本 Action 只用 `docker buildx build`（发布用 `buildx imagetools create`，' +
        '没有经典构建回退）。\n' +
        `${DOCKER_HELP}`,
    );
  }

  if (state.dryRun) {
    warnings.push('dry run：不登录、不推送，只验证构建（--output type=cacheonly）');
  }

  return { errors, warnings, builds };
}

async function main() {
  const env = process.env;
  const state = JSON.parse(readFileSync(statePath(env), 'utf8'));
  const { errors, warnings, builds } = preflightChecks({ state, env });

  for (const warning of warnings) process.stderr.write(`[WARN] ${warning}\n`);
  if (errors.length > 0) {
    for (const error of errors) process.stderr.write(`[ERROR] ${error}\n`);
    process.stderr.write('预检未通过，已中止。\n');
    process.exit(1);
  }

  const tags = builds.reduce((sum, build) => sum + build.images.reduce((count, image) => count + image.tags.length, 0), 0);
  process.stdout.write(
    `预检通过：${state.version} → ${state.images.map((image) => image.base).join('、')}；` +
      `${builds.length} 个变体、${tags} 个 tag` +
      `${state.options.push ? '' : '（不推送）'}\n`,
  );
  if (env.GITHUB_OUTPUT) {
    const { appendFileSync } = await import('node:fs');
    appendFileSync(env.GITHUB_OUTPUT, `dockerfile_count=${builds.length}\n`);
  }
}

if (IS_DIRECT) {
  try {
    await main();
  } catch (error) {
    process.stderr.write(`预检异常：${error.message}\n`);
    process.exit(1);
  }
}
