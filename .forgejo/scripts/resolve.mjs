// resolve.mjs — derive the release identity and the whole build plan skeleton
// from the environment, and persist it for the later steps.
//
// The rules match the npm-publish Action of this template library: the tag that
// triggered the run is the version, and a hand-typed workflow_dispatch version
// must agree with an existing tag, so a typo cannot publish the wrong version.
//
// The state file lives in $RUNNER_TEMP (default /tmp) and deliberately contains
// no credential: registries are recorded by token *variable name* only.

import { appendFileSync, readFileSync, writeFileSync, realpathSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  buildOptionsFrom,
  imagesFrom,
  parseVariants,
  platformsFor,
  readOptional,
  registriesFrom,
  variantSelection,
  variantTableRaw,
} from './config.mjs';
import { goProxyFrom, resolveEndpoint } from './endpoints.mjs';

export const SEMVER = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?$/;

/** Where the shared state for one run lives. */
export function statePath(env = process.env) {
  return join(env.RUNNER_TEMP || '/tmp', 'docker-publish.json');
}

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

/**
 * Build arguments this Action can fill in by itself, because their values are
 * known only at run time:
 *
 *   * `VERSION`      — the tag (what people write by hand with the official trio:
 *                      `build-args: VERSION=${{ steps.meta.outputs.version }}`);
 *   * `GOPROXY`      — `GOPROXY`, so a Dockerfile's `ARG GOPROXY`
 *                      reaches an internal Athens without extra configuration;
 *   * `NPM_REGISTRY` — the registry `classifyEndpoints()`
 *                      classified out of `NPM_PROXY`;
 *   * `PIP_INDEX_URL` — the Python package index `classifyEndpoints()`
 *                      classified out of `PIP_PROXY`, expanded to the `/simple`
 *                      or `/index` path the endpoint actually serves;
 *   * `APT_PROXY` / `APK_PROXY` — the mirror roots the Dockerfiles rewrite their
 *                      package sources with (the same two build args
 *                      build-image.sh passes). `APT_PROXY` is only filled in when
 *                      it was *classified as a mirror*: a forward proxy is not a
 *                      repository, and handing it to a Dockerfile that does
 *                      `s|^https?://|<root>/|` would produce a broken sources
 *                      list. A real proxy reaches the build through BuildKit's
 *                      HTTP(S)_PROXY forwarding instead;
 *   * `APK_PROXY` also answers to the explicit `APK_REPO`, which is a repository
 *     by definition (no probing) and wins over the apt mirror — this mirrors what
 *     build-image.sh does with `APK_PROXY="${APK_PROXY:-${APK_REPO:-${APT_PROXY}}}"`;
 *   * `YUM_REPO` — passed through as `--build-arg YUM_REPO=<value>` for a
 *     Dockerfile that rewrites its dnf/yum repositories.
 *
 * An explicit `DOCKER_BUILD_ARGS` entry always wins, because that is the
 * repository saying what it wants. Nothing is injected when the source variable
 * is empty, so a repository that sets none of them sees exactly the same
 * command line as before.
 */
export function withDefaultBuildArgs(options, { version, env = process.env, endpoints = {} }) {
  const candidates = [
    ['VERSION', version],
    ['GOPROXY', goProxyFrom(env)],
    ['NPM_REGISTRY', readOptional(endpoints.npmRegistry)],
    ['PIP_INDEX_URL', readOptional(endpoints.pipIndex)],
    ['APT_PROXY', readOptional(endpoints.aptMirror)],
    // An explicit APK_REPO is a repository by definition and wins; otherwise the
    // Alpine Dockerfile reuses the apt mirror root (the usual cache serves both).
    ['APK_PROXY', readOptional(env.APK_REPO) || readOptional(endpoints.aptMirror)],
    ['YUM_REPO', readOptional(env.YUM_REPO)],
  ];
  const injected = candidates
    .filter(([key, value]) => value !== undefined && value !== null && String(value) !== '')
    .filter(([key]) => !options.buildArgs.some((arg) => arg.key === key))
    .map(([key, value]) => ({ key, value: String(value) }));
  return { ...options, buildArgs: [...injected, ...options.buildArgs] };
}

export function stripTagPrefix(tag) {
  let value = String(tag || '').trim();
  if (value.startsWith('refs/tags/')) value = value.slice('refs/tags/'.length);
  if (value.startsWith('v')) value = value.slice(1);
  return value;
}

export function tagVersionFromRef(ref) {
  const match = /^refs\/tags\/(.+)$/.exec(ref || '');
  return match ? match[1] : '';
}

/**
 * A `${{ … }}` the runner never expanded.
 *
 * Forgejo's expression evaluator leaves a context it does not know as *literal
 * text*: on a tag push `inputs` does not exist, so `INPUT_VERSION` arrives as the
 * string `${{ inputs.version }}` rather than as an empty string. Reading that as
 * "the typed version" made every tag run fail the consistency check.
 */
export const UNEXPANDED_EXPRESSION = /^\s*\$\{\{[^}]*\}\}\s*$/;

/**
 * The `inputs` object of the event payload, when the runner shipped one.
 *
 * The runner always writes the event to `$GITHUB_EVENT_PATH`, and for a
 * workflow_dispatch that payload carries the inputs — which is a fallback that
 * does not depend on the expression engine interpolating anything.
 */
export function readEventInputs(env = process.env, readFile = readFileSync) {
  const file = readOptional(env.GITHUB_EVENT_PATH);
  if (!file) return {};
  try {
    const payload = JSON.parse(readFile(file, 'utf8'));
    return payload && typeof payload.inputs === 'object' && payload.inputs ? payload.inputs : {};
  } catch {
    return {};
  }
}

/**
 * One dispatch input: the environment variable first, then the event payload.
 *
 * An unexpanded expression is treated as "not provided" — with a warning, so a
 * runner that cannot evaluate `inputs.*` is visible instead of silently
 * changing the release version.
 */
export function dispatchInput(env, name, eventInputs = {}, { warn = () => {} } = {}) {
  const key = `INPUT_${name}`;
  const raw = readOptional(env[key]);
  const fromEvent = readOptional(eventInputs[name.toLowerCase()] ?? eventInputs[name]);
  if (raw && UNEXPANDED_EXPRESSION.test(raw)) {
    warn(
      `${key} 是未被展开的表达式（${raw}）：该 runner 没有对 inputs.* 求值，` +
        (fromEvent
          ? `已改用事件载荷里的值（${fromEvent}）`
          : '事件载荷里也没有该输入，按未填写处理'),
    );
    return fromEvent;
  }
  return raw || fromEvent;
}

/**
 * The release identity is the tag that triggered the run: a semver tag, with an
 * optional `v`, and optionally a pre-release suffix. Anything else is refused —
 * there is no escape hatch, because a mistyped tag is how wrong versions ship.
 */
export function resolveRelease(env = process.env, { variantsOverride, warn = () => {} } = {}) {
  const eventInputs = readEventInputs(env);
  const inputVersion = dispatchInput(env, 'VERSION', eventInputs, { warn });
  const inputVariants = dispatchInput(env, 'VARIANTS', eventInputs, { warn });
  const inputDryRun = dispatchInput(env, 'DRY_RUN', eventInputs, { warn });
  const refName = readOptional(env.GITHUB_REF_NAME);
  const ref = readOptional(env.GITHUB_REF);
  const isRefTag = ref.startsWith('refs/tags/');
  // A dispatch can be started from a branch, where there is no tag to inherit:
  // then the typed version IS the release, and it must be typed explicitly.
  if (inputVersion && isRefTag && stripTagPrefix(inputVersion) !== stripTagPrefix(refName)) {
    throw new Error(
      `手工输入的版本（${inputVersion}）与触发 ref（${refName || '<none>'}）不一致，已中止以避免误发`,
    );
  }
  if (!inputVersion && !isRefTag && !tagVersionFromRef(ref)) {
    throw new Error(
      `当前 ref 是 ${ref || '<empty>'}，不是 tag：请推送 tag，或在 workflow_dispatch 里填写 version（要与已有 tag 的版本一致）`,
    );
  }
  const rawTag = inputVersion || refName || tagVersionFromRef(ref);
  if (!rawTag) throw new Error('无法确定版本：GITHUB_REF_NAME 为空');
  const versionSource = inputVersion ? 'input' : 'ref';

  const version = stripTagPrefix(rawTag);
  if (!SEMVER.test(version)) {
    throw new Error(`无法从 tag 解析出合法版本号：${rawTag}（需要形如 v1.2.3 或 1.2.3-rc.1 的 tag）`);
  }
  const prerelease = version.includes('-');

  const repo = readOptional(env.GITHUB_REPOSITORY);
  const serverUrl = readOptional(env.GITHUB_SERVER_URL);
  const runNumber = readOptional(env.GITHUB_RUN_NUMBER);
  const sha = readOptional(env.GITHUB_SHA);
  const dryRun = ['1', 'true', 'yes', 'on'].includes(inputDryRun.toLowerCase());

  const variants = variantSelection(
    // The fifth column of the table is what decides who owns `latest`; the table
    // itself comes from DOCKER_VARIANTS, else <forgejo_dir>/variants.txt, else
    // the built-in default (the same three sources plan-matrix uses).
    parseVariants(readOptional(env.DOCKER_VARIANTS) || variantsOverride || variantTableRaw(env)),
    inputVariants,
  );
  const registries = registriesFrom(env);
  const images = imagesFrom(env, registries, repo.split('/').pop() || '', { preview: dryRun });

  return {
    version,
    tag: rawTag,
    versionSource,
    prerelease,
    semver: true,
    sha,
    shortSha: sha.slice(0, 7),
    repo,
    repoName: repo.split('/').pop() || '',
    serverUrl,
    runUrl: `${serverUrl}/${repo}/actions/runs/${runNumber}`,
    runNumber,
    runAttempt: readOptional(env.GITHUB_RUN_ATTEMPT),
    eventName: readOptional(env.GITHUB_EVENT_NAME),
    refName,
    ref,
    // A manual dispatch names an existing tag rather than a ref, so it is
    // treated as a tag event for every tagging rule.
    isTagEvent: ref.startsWith('refs/tags/') || readOptional(env.GITHUB_EVENT_NAME) === 'workflow_dispatch',
    isBranchEvent: ref.startsWith('refs/heads/'),
    isPrEvent: ref.startsWith('refs/pull/'),
    dryRun,
    variants,
    registries,
    images,
    // The build args that need the network (APT_PROXY / NPM_PROXY / PIP_PROXY
    // classification) are filled in by main(), which can await the probes.
    options: buildOptionsFrom(env, { dryRun }),
    builds: [],
    results: [],
  };
}

/**
 * Classify the three endpoint variables that may be a mirror or a proxy.
 *
 * A mirror is a *repository*, not a proxy: `APT_PROXY` pointed at an internal
 * Debian mirror must reach a Dockerfile as a mirror root (the Dockerfile rewrites
 * its sources with it), while a genuine forward proxy reaches the build through
 * BuildKit's HTTP(S)_PROXY forwarding and must not be passed as a repository.
 * `NPM_PROXY` is classified the same way and injected as `--build-arg
 * NPM_REGISTRY`; `PIP_PROXY` becomes `--build-arg PIP_INDEX_URL`, with the
 * `/simple` or `/index` path the endpoint answered on.
 */
export async function classifyEndpoints(env = process.env, { resolve = resolveEndpoint } = {}) {
  const endpoints = {};
  const apt = await resolve('apt-get', env);
  if (apt.source === 'proxy-as-mirror') endpoints.aptMirror = envValueOf(env, 'APT_PROXY');
  const npm = await resolve('npm', env);
  if (npm.source === 'proxy-as-mirror') endpoints.npmRegistry = npm.mirror[0];
  const pip = await resolve('pip', env);
  if (pip.source === 'proxy-as-mirror') endpoints.pipIndex = pip.mirror[0];
  return endpoints;
}

/** The raw configured value, without a trailing slash. */
function envValueOf(env, name) {
  return readOptional(env[name]).replace(/\/+$/, '');
}

async function main() {
  const state = resolveRelease(process.env);
  const endpoints = await classifyEndpoints(process.env);
  state.options = withDefaultBuildArgs(state.options, { version: state.version, env: process.env, endpoints });
  writeFileSync(statePath(process.env), JSON.stringify(state, null, 2));

  const emit = (key, value) => {
    if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${value}\n`);
    process.stdout.write(`${key}=${value}\n`);
  };
  emit('version', state.version);
  emit('tag', state.tag);
  emit('version_source', state.versionSource);
  emit('prerelease', String(state.prerelease));
  emit('dry_run', String(state.dryRun));
  emit('push', String(state.options.push));
  emit('variants', state.variants.map((variant) => variant.name).join(','));
  emit('images', state.images.map((image) => image.base).join(','));
  process.stdout.write(
    `tag=${state.tag} version=${state.version} 来源=${state.versionSource === 'input' ? 'workflow_dispatch 输入' : 'ref'} ` +
      `prerelease=${state.prerelease} dry-run=${state.dryRun} push=${state.options.push}\n`,
  );
  process.stdout.write(
    `变体：${state.variants
      .map((variant) => {
        const platforms = platformsFor(variant, state.options);
        return (
          `${variant.name}(${variant.dockerfile}${variant.target ? `#${variant.target}` : ''}` +
          `${variant.isDefault ? ',默认' : ''},${platforms.join('+') || '本机'})`
        );
      })
      .join(' ')}\n`,
  );
  process.stdout.write(
    `registry：${state.registries.map((entry) => `${entry.id}=${entry.enabled ? `启用(${entry.user})` : '未配置'}`).join(' ')}\n`,
  );
  const injected = state.options.buildArgs.map((arg) => `${arg.key}=${arg.value}`).join(' ');
  process.stdout.write(`构建参数：${injected}\n`);
}

if (IS_DIRECT) {
  try {
    await main();
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exit(1);
  }
}
