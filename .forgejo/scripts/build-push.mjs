// build-push.mjs — the Forgejo port of docker/build-push-action, limited to the
// two supported registries, plus the staging step that makes publishing a
// separate, retryable job.
//
// Three things this module is strict about:
//
//   1. One `docker buildx build` per variant. Each variant compiles its own
//      artifact against its own base image; nothing is shared between variants,
//      and no variant is ever produced by retagging another one. That is what
//      makes "alpine" and "trixie-slim" honest builds rather than one build with
//      two tags.
//   2. It pushes a STAGING tag, never the final ones. The final tags (including
//      `latest`) are created by publish.mjs once every variant and architecture
//      exists, so a half-finished run can never move `latest` and a failed
//      publish can be retried without rebuilding.
//   3. buildx, always. There is no classic-builder fallback: the job container
//      provides the CLI and the plugin, and preflight refuses to run without
//      them.
//
// Platforms and tags are orthogonal: variants are tags, platforms are manifest
// list entries. When more than one platform is requested, a `docker-container`
// builder is created for the run and removed afterwards.
//
// Everything that crosses the network — the builder image pull (Docker Hub), the
// staging-existence check, the multi-architecture push — is retried a bounded
// number of times on transport failures (see retry.mjs). An auth refusal or a
// compile error is never retried: a later attempt cannot fix either.
//
// Re-running the workflow is cheap by design: a variant whose staging tag
// already exists is not rebuilt (DOCKER_FORCE_BUILD=1 overrides that), so the
// usual "publish failed, push the tag again" flow only re-runs publication.

import { existsSync, mkdtempSync, readFileSync, writeFileSync, realpathSync } from 'node:fs';
import { spawn, spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { isTruthy, platformsFor, readOptional, stagingRefsFor } from './config.mjs';
import { probeDocker } from './endpoints.mjs';
import { statePath } from './resolve.mjs';
import {
  classifyFailure,
  delayFor,
  retryConfig,
  retryLine,
  retrySync,
  sleepSync,
} from './retry.mjs';

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

/** `--label k=v` pairs, in a stable order. */
export function labelArgs(labels = {}) {
  return Object.entries(labels).flatMap(([key, value]) => ['--label', `${key}=${value}`]);
}

/**
 * Proxy variables BuildKit accepts as predefined build args. They must be passed
 * explicitly: the CLI only auto-populates proxy *build args* from its own
 * `~/.docker/config.json` (`proxies`), never from its environment, so a job env
 * carrying HTTP_PROXY alone would not reach a `RUN` step.
 */
export const PROXY_BUILD_ARGS = ['HTTP_PROXY', 'HTTPS_PROXY', 'NO_PROXY', 'ALL_PROXY'];

/** The proxy variables actually present in the environment, in a stable order. */
export function proxiesFrom(env = {}) {
  return PROXY_BUILD_ARGS.map((key) => [key, readOptional(env[key])]).filter(([, value]) => value !== '');
}

export function buildxCreateArgv(name, image = '', env = {}) {
  const argv = ['buildx', 'create', '--use', '--name', name, '--driver', 'docker-container'];
  // An internal registry mirror can be named with DOCKER_BUILDKIT_IMAGE: the
  // builder container is where BuildKit itself is pulled from.
  if (readOptional(image)) argv.push('--driver-opt', `image=${readOptional(image)}`);
  // buildkitd pulls the base images itself, so it needs the proxies in its own
  // environment; `env.*` is the docker-container driver option that sets it.
  for (const [key, value] of proxiesFrom(env)) argv.push('--driver-opt', `env.${key}=${value}`);
  return argv;
}

export function buildxRemoveArgv(name) {
  return ['buildx', 'rm', name];
}

/**
 * The build args of one variant: the per-variant ones from the variant table
 * (e.g. `NODE_IMAGE=node:lts-alpine`) plus the globally injected ones.
 *
 * `options.buildArgs` wins on a key collision: it holds what `resolve` injected
 * (`VERSION`, `GOPROXY`, …) and what the repository wrote in `DOCKER_BUILD_ARGS`,
 * which is the more explicit statement of intent.
 */
export function effectiveBuildArgs(build, options, env = {}) {
  const merged = new Map();
  for (const arg of build.buildArgs || []) merged.set(arg.key, arg);
  // Proxies sit under both the variant table and `DOCKER_BUILD_ARGS`: an explicit
  // `--build-arg` always wins over the ambient proxy.
  for (const [key, value] of proxiesFrom(env)) {
    if (!merged.has(key)) merged.set(key, { key, value });
  }
  for (const arg of options.buildArgs || []) merged.set(arg.key, arg);
  return [...merged.values()];
}

/**
 * The full `docker buildx build` command line for one variant.
 *
 * `images` overrides the tags to attach: the build step passes the staging refs
 * (`stagingRefsFor`), so the same builder invocation serves both the staging
 * push and the tag model `meta` computed.
 *
 * `--push` and `--output type=cacheonly` are mutually exclusive outputs: the
 * first publishes, the second validates the build without touching the image
 * store, which is what a dry run wants.
 */
export function buildxArgv({ build, options, metadataFile, builder = '', images = null, env = {} }) {
  const argv = ['buildx', 'build'];
  if (builder) argv.push('--builder', builder);
  argv.push('--file', build.dockerfile);
  if (build.target) argv.push('--target', build.target);
  for (const image of images || build.images) for (const tag of image.tags) argv.push('--tag', tag);
  argv.push(...labelArgs(build.labels));
  // Platforms are per variant: the variant table's seventh column wins, the
  // global DOCKER_PLATFORMS is the fallback. A single-platform build needs no
  // docker-container builder; a multi-platform one does (see main()).
  const platforms = platformsFor(build, options);
  if (platforms.length > 0) argv.push('--platform', platforms.join(','));
  for (const { key, value } of effectiveBuildArgs(build, options, env)) argv.push('--build-arg', `${key}=${value}`);
  // Attestations are always off: they add manifest entries some registries and
  // clients still choke on, and this Action has no knob for them.
  argv.push('--provenance=false');
  if (metadataFile) argv.push('--metadata-file', metadataFile);
  if (options.push) argv.push('--push');
  else argv.push('--output', 'type=cacheonly');
  argv.push(options.context);
  return argv;
}

/** `containerimage.digest` out of a buildx --metadata-file. */
export function digestFromMetadata(json) {
  return String(json?.['containerimage.digest'] ?? '').trim();
}

/** Run docker with the log streamed to ours (builds must show progress). */
export function runDocker(args, { env = process.env, run = spawnSync, capture = false } = {}) {
  process.stdout.write(`$ docker ${args.join(' ')}\n`);
  const result = run('docker', args, {
    encoding: capture ? 'utf8' : undefined,
    stdio: capture ? ['ignore', 'pipe', 'pipe'] : ['ignore', 'inherit', 'inherit'],
    env,
  });
  return {
    status: result.status === 0 ? 0 : result.status ?? 1,
    stdout: String(result.stdout || ''),
    stderr: String(result.stderr || result.error?.message || ''),
  };
}

/**
 * Run one registry-facing docker command with bounded retries.
 *
 * Only transport failures are retried (see retry.mjs): a timed-out push, a 503
 * from the token/registry service, a rate-limit answer. An auth refusal or a
 * "manifest unknown" answer returns at once — a later attempt cannot fix it.
 * `label` names the action in the retry log line.
 */
export function runDockerRetry(args, {
  env = process.env,
  run = spawnSync,
  label = `docker ${args.slice(0, 2).join(' ')}`,
  attempts,
  delayMs,
  sleep = sleepSync,
  onRetry,
} = {}) {
  const config = retryConfig(env);
  const outcome = retrySync(
    () => {
      const result = runDocker(args, { env, run, capture: true });
      return { ok: result.status === 0, text: `${result.stdout}\n${result.stderr}`, ...result };
    },
    {
      attempts: attempts ?? config.attempts,
      delayMs: delayMs ?? config.delayMs,
      maxDelayMs: config.maxDelayMs,
      sleep,
      onRetry: onRetry || ((info) => process.stderr.write(`${retryLine(label, info)}\n`)),
    },
  );
  if (!outcome.ok) {
    // The final attempt's output, once: the retry lines only carry the verdict.
    const detail = `${outcome.stdout || ''}${outcome.stderr || ''}`.trim();
    if (detail) process.stderr.write(`${detail.slice(-4000)}\n`);
  }
  return outcome;
}

/**
 * Stream one docker command (a build must show progress) and keep the tail of
 * its output, so a failure can be classified without swallowing the live log.
 */
export function runDockerStream(args, { env = process.env, spawnImpl = spawn } = {}) {
  process.stdout.write(`$ docker ${args.join(' ')}\n`);
  return new Promise((resolve) => {
    let tail = '';
    const collect = (chunk, stream) => {
      const text = String(chunk);
      if (!text) return;
      stream.write(text);
      tail = `${tail}${text}`.slice(-16_384);
    };
    let child;
    try {
      child = spawnImpl('docker', args, { env, stdio: ['ignore', 'pipe', 'pipe'] });
    } catch (error) {
      resolve({ status: 1, tail: String(error?.message || error) });
      return;
    }
    child.stdout?.setEncoding('utf8');
    child.stderr?.setEncoding('utf8');
    child.stdout?.on('data', (chunk) => collect(chunk, process.stdout));
    child.stderr?.on('data', (chunk) => collect(chunk, process.stderr));
    child.on('error', (error) => resolve({ status: 1, tail: `${tail}\n${error?.message || error}` }));
    child.on('close', (code) => resolve({ status: code === 0 ? 0 : code ?? 1, tail }));
  });
}

/** Total attempts for one `buildx build`; `DOCKER_BUILD_RETRY_ATTEMPTS` (default 2). */
export function buildAttempts(env = process.env) {
  const raw = readOptional(env.DOCKER_BUILD_RETRY_ATTEMPTS);
  if (raw === '') return 2;
  const number = Number(raw);
  if (!Number.isFinite(number)) return 2;
  return Math.min(5, Math.max(1, Math.floor(number)));
}

const sleepAsync = (ms) => (ms > 0 ? new Promise((resolve) => setTimeout(resolve, ms)) : Promise.resolve());

/**
 * Is a registry ref present? `exists`, `missing` or `unknown`.
 *
 * The third state matters: a transient network error must not be reported as
 * "this variant was never staged" (build would rebuild needlessly) nor as
 * "staging missing" (publish would abort with the wrong advice).
 */
export function refState(ref, { env = process.env, run = spawnSync, sleep = sleepSync, attempts, delayMs, onRetry } = {}) {
  const config = retryConfig(env);
  const outcome = retrySync(
    () => {
      const result = runDocker(['buildx', 'imagetools', 'inspect', ref], { env, run, capture: true });
      return { ok: result.status === 0, text: `${result.stdout}\n${result.stderr}` };
    },
    {
      attempts: attempts ?? config.attempts,
      delayMs: delayMs ?? config.delayMs,
      maxDelayMs: config.maxDelayMs,
      sleep,
      onRetry: onRetry || ((info) => process.stderr.write(`${retryLine(`查询 ${ref}`, info)}\n`)),
    },
  );
  if (outcome.ok) return 'exists';
  // "not found"/"manifest unknown" is a result; anything else that exhausted the
  // retries is a transport problem.
  return classifyFailure(outcome.text) === 'permanent' ? 'missing' : 'unknown';
}

/** Does a ref already exist in its registry? (registry-only buildx call) */
export function refExists(ref, deps = {}) {
  return refState(ref, deps) === 'exists';
}

/**
 * Per-ref state of one variant's staging refs, plus the aggregated verdict.
 * `unknown` is kept apart from `missing` so the caller can tell a network
 * outage from a genuinely absent staging image.
 */
export function stagingStatus(refs = [], deps = {}) {
  const states = (refs || []).map((entry) => ({ ref: entry.ref, state: refState(entry.ref, deps) }));
  return {
    total: states.length,
    complete: states.length > 0 && states.every((entry) => entry.state === 'exists'),
    missing: states.filter((entry) => entry.state === 'missing').map((entry) => entry.ref),
    unknown: states.filter((entry) => entry.state === 'unknown').map((entry) => entry.ref),
  };
}

/**
 * Are all staging refs of one variant already pushed? When they are, the variant
 * was built by an earlier run of this same version+sha and can be skipped —
 * which is what makes "publish failed, re-run" cheap.
 */
export function stagingComplete(refs = [], deps = {}) {
  return stagingStatus(refs, deps).complete;
}

function readDigest(metadataFile) {
  if (!existsSync(metadataFile)) return '';
  try {
    return digestFromMetadata(JSON.parse(readFileSync(metadataFile, 'utf8')));
  } catch {
    return '';
  }
}

/**
 * Build one variant and push it to its staging tag.
 *
 * `buildx build --push` is the most expensive call of the run, so it gets its
 * own (small) budget: one retry by default, and only for a transport failure —
 * a compile error or an auth refusal ends the variant immediately. Re-pushing
 * the same staging tag is idempotent (version+sha+variant decide the tag), so a
 * retry can never publish a different artifact.
 */
async function buildOneVariant({ build, options, env, builder, workDir, staging }) {
  const config = retryConfig(env);
  const attempts = buildAttempts(env);
  const metadataFile = join(workDir, `metadata-${build.variant}.json`);
  const argv = buildxArgv({ build, options, metadataFile, builder, images: staging, env });

  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    const result = await runDockerStream(argv, { env });
    if (result.status === 0) {
      return {
        variant: build.variant,
        builder: 'buildx',
        digest: readDigest(metadataFile),
        platforms: platformsFor(build, options),
        staging: staging.map((entry) => entry.ref),
        tagNames: build.tagNames,
        images: build.images,
        skipped: false,
        attempts: attempt,
      };
    }
    const kind = classifyFailure(result.tail);
    const retryable = (kind === 'network' || kind === 'rate-limit') && attempt < attempts;
    if (!retryable) {
      const hint = kind === 'network' || kind === 'rate-limit' ? '，网络错误且重试次数已用尽' : '';
      throw new Error(`变体 ${build.variant} 构建失败（docker buildx build 退出码 ${result.status}${hint}）`);
    }
    const wait = delayFor(attempt, config, kind);
    process.stderr.write(`${retryLine(`变体 ${build.variant} 构建`, { attempt, attempts, kind, wait })}\n`);
    await sleepAsync(wait);
  }
  throw new Error(`变体 ${build.variant} 构建失败（重试已用尽）`);
}

async function main() {
  const env = process.env;
  const state = JSON.parse(readFileSync(statePath(env), 'utf8'));
  if (!Array.isArray(state.builds) || state.builds.length === 0) {
    process.stderr.write('没有构建计划：请先运行 meta 步骤\n');
    process.exit(1);
  }

  const docker = probeDocker(env);
  if (!docker.cli || !docker.daemon || !docker.buildx) {
    process.stderr.write('docker / buildx 不可用，无法构建（preflight 会给出前置条件清单）\n');
    process.exit(1);
  }

  const workDir = mkdtempSync(join(tmpdir(), 'docker-build-push-'));
  // The step owns the results: a second invocation in the same job (or a manual
  // re-run of this step) must not append duplicates to the summary.
  state.results = [];
  // A docker-container builder is required as soon as ONE variant asks for more
  // than one platform (the default builder cannot produce a manifest list).
  const needsBuilder = state.builds.some((build) => platformsFor(build, state.options).length > 1);
  const builder = needsBuilder ? `forgejo-docker-publish-${process.pid}` : '';
  const force = isTruthy(env.DOCKER_FORCE_BUILD);
  let createdBuilder = false;

  try {
    if (builder) {
      // The builder image comes from Docker Hub; a failed create may also have
      // left a half-registered builder behind, so a retry removes it first.
      const createConfig = retryConfig(env);
      const created = retrySync(
        () => {
          const result = runDocker(buildxCreateArgv(builder, env.DOCKER_BUILDKIT_IMAGE, env), { env, run: spawnSync, capture: true });
          return { ok: result.status === 0, text: `${result.stdout}\n${result.stderr}`, status: result.status };
        },
        {
          attempts: createConfig.attempts,
          delayMs: createConfig.delayMs,
          maxDelayMs: createConfig.maxDelayMs,
          onRetry: (info) => {
            runDocker(buildxRemoveArgv(builder), { env, run: spawnSync, capture: true });
            process.stderr.write(`${retryLine(`创建 buildx builder ${builder}`, info)}\n`);
          },
        },
      );
      if (created.status !== 0) throw new Error(`创建 buildx builder 失败（${builder}）`);
      createdBuilder = true;
    }
    for (const build of state.builds) {
      const options = state.dryRun ? { ...state.options, push: false } : state.options;
      const platforms = platformsFor(build, options);
      const staging = stagingRefsFor(build, state);
      process.stdout.write(
        `==> ${build.variant}${build.isDefault ? '（默认）' : ''}：${build.dockerfile}${build.target ? `#${build.target}` : ''}` +
          ` platforms=${platforms.join(',') || '（本机）'}\n`,
      );

      // Nothing was pushed in a dry run, so there is nothing to reuse.
      if (options.push && !force) {
        const status = stagingStatus(staging, { env, run: spawnSync });
        if (status.unknown.length > 0) {
          process.stderr.write(
            `[WARN] 查询 staging 状态时遇到网络错误（${status.unknown.join('、')}），按「未 staged」处理并重建；` +
              '若这是误判，可稍后重跑（或设 DOCKER_FORCE_BUILD=1 跳过查询）\n',
          );
        }
        if (status.complete) {
          const refs = staging.map((entry) => entry.ref).join('、');
          process.stdout.write(`    已存在 staging 镜像，跳过构建（DOCKER_FORCE_BUILD=1 可强制重建）：${refs}\n`);
          state.results.push({
            variant: build.variant,
            builder: 'buildx',
            digest: '',
            platforms,
            staging: staging.map((entry) => entry.ref),
            tagNames: build.tagNames,
            images: build.images,
            skipped: true,
          });
          writeFileSync(statePath(env), JSON.stringify(state, null, 2));
          continue;
        }
      }

      const result = await buildOneVariant({ build, options, env, builder, workDir, staging });
      state.results.push(result);
      writeFileSync(statePath(env), JSON.stringify(state, null, 2));
      process.stdout.write(`    完成：${result.digest || '(未取到 digest)'}\n`);
      process.stdout.write(`    staging：${result.staging.join('、')}\n`);
    }
  } finally {
    if (createdBuilder) {
      const removed = runDocker(buildxRemoveArgv(builder), { run: spawnSync, env });
      if (removed.status !== 0) process.stderr.write(`[WARN] 清理 buildx builder 失败：${builder}\n`);
    }
  }

  const built = state.results.filter((result) => !result.skipped).length;
  process.stdout.write(
    `\n构建完成：${state.results.length} 个变体（新建 ${built}、复用 ${state.results.length - built}）；` +
      `${state.options.push && !state.dryRun ? '已推送到 staging，最终标签由 publish 作业发布' : '未推送'}\n`,
  );
}

if (IS_DIRECT) {
  try {
    await main();
  } catch (error) {
    process.stderr.write(`构建/推送失败：${error.message}\n`);
    process.exit(1);
  }
}
