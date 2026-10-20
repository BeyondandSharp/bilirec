// config.mjs — turn the repository's variables into one normalized config.
//
// Everything the Action can be told arrives as environment variables (the
// workflow maps secrets/vars into `env:`), so this module is pure: it reads a
// plain object and returns data. No process state, no side effects, no tokens.
//
// Deliberate design points:
//   * a registry is enabled by the presence of its token — nothing else;
//   * image bases are derived from the repository unless DOCKER_META_IMAGES
//     says otherwise, and are always lowercased (ghcr.io requires it);
//   * variants are data, not code: the variant table describes how each variant
//     is built, and every variant is built by its own `docker buildx build`.
//
// Tokens never leave this module inside a returned structure: entries carry the
// *name* of the variable holding the token, so a caller cannot accidentally
// serialise a credential into the state file.

import { readFileSync } from 'node:fs';

/**
 * The built-in variant table: the Alpine and Debian (trixie) builds, each for
 * both supported architectures. It is only the fallback — a repository normally
 * owns this table in `.forgejo/variants.txt` or in the `DOCKER_VARIANTS`
 * variable, so the shipped workflow stays free of project names.
 */
export const DEFAULT_VARIANTS = [
  'alpine|Dockerfile.alpine||-alpine|false||linux/amd64,linux/arm64',
  'trixie-slim|Dockerfile.trixie-slim||-trixie-slim|true||linux/amd64,linux/arm64',
].join('\n');

/**
 * The variant table this run should use, from the same three sources every
 * program shares (so `plan-matrix`, `prepare` and `resolve` can never disagree):
 *
 *   1. `DOCKER_VARIANTS` (repository variable / workflow env) — highest;
 *   2. the file `DOCKER_VARIANTS_FILE` points at (normally
 *      `<forgejo_dir>/variants.txt`, exported by locate-action);
 *   3. the built-in DEFAULT_VARIANTS.
 */
export function variantTableRaw(env = process.env, { readFile = readFileSync } = {}) {
  const explicit = readOptional(env.DOCKER_VARIANTS);
  if (explicit) return explicit;
  const file = readOptional(env.DOCKER_VARIANTS_FILE);
  if (file) {
    try {
      const text = readFile(file, 'utf8');
      if (readOptional(text)) return text;
    } catch {
      // No file: fall through to the built-in default.
    }
  }
  return DEFAULT_VARIANTS;
}

/** Registries this Action knows how to log in to and push to. */
export const REGISTRIES = {
  ghcr: { id: 'ghcr', registry: 'ghcr.io', tokenVar: 'GHCR_TOKEN' },
  dockerhub: { id: 'dockerhub', registry: 'docker.io', tokenVar: 'DOCKERHUB_TOKEN' },
};

export function readOptional(value) {
  return String(value ?? '').trim();
}

/** Newline / comma / semicolon separated list, `#` comments dropped. */
export function splitList(raw) {
  return String(raw ?? '')
    .split(/[\n,;]+/)
    .map((item) => item.trim())
    .filter((item) => item !== '' && !item.startsWith('#'));
}

/**
 * Newline-separated list. Used where the value itself may contain a comma:
 * `GOPROXY=http://proxy,direct` is one build arg, not two.
 */
export function splitLines(raw) {
  return String(raw ?? '')
    .split('\n')
    .map((item) => item.trim())
    .filter((item) => item !== '' && !item.startsWith('#'));
}

/**
 * `KEY=VALUE` lines (build args, labels). A line without `=` is an error: it is
 * almost always a typo, and silently dropping it would build a different image
 * than the one asked for.
 */
export function parseKeyValues(raw, what = 'KEY=VALUE') {
  return splitLines(raw).map((line) => {
    const index = line.indexOf('=');
    if (index <= 0) throw new Error(`${what} 需要 KEY=VALUE 形式（每行一条），收到：${line}`);
    return { key: line.slice(0, index).trim(), value: line.slice(index + 1) };
  });
}

const VARIANT_NAME = /^[a-z0-9][a-z0-9._-]*$/;

/**
 * The per-variant build arguments of the sixth column: `KEY=VALUE`, several
 * separated by `;`.
 *
 * This is how a repository tells the Action which toolchain image a variant is
 * built with — the Dockerfile declares `ARG NODE_IMAGE` and does
 * `FROM ${NODE_IMAGE}`, and the value travels with the variant:
 *
 *   alpine|Dockerfile.alpine||-alpine|false|NODE_IMAGE=node:lts-alpine
 *   trixie-slim|Dockerfile.trixie-slim||-trixie-slim|true|NODE_IMAGE=node:lts-trixie-slim
 */
export function parseVariantBuildArgs(raw) {
  return String(raw ?? '')
    .split(';')
    .map((item) => item.trim())
    .filter((item) => item !== '')
    .map((item) => {
      const index = item.indexOf('=');
      if (index <= 0) throw new Error(`变体的构建参数需要 KEY=VALUE 形式（多条用 ; 分隔），收到：${item}`);
      return { key: item.slice(0, index).trim(), value: item.slice(index + 1) };
    });
}

/**
 * The architectures of a platform list, e.g. `linux/amd64,linux/arm64`.
 * A bare `linux/arm/v7` is accepted too (three segments).
 */
const PLATFORM = /^[a-z0-9]+\/[a-z0-9]+(?:\/[a-z0-9]+)?$/;

export function parsePlatforms(raw) {
  return String(raw ?? '')
    .split(',')
    .map((item) => item.trim())
    .filter((item) => item !== '')
    .map((item) => {
      if (!PLATFORM.test(item)) throw new Error(`平台名非法：${item}（应形如 linux/amd64 或 linux/arm/v7）`);
      return item;
    });
}

/**
 * The platforms one variant is built for: its own seventh column when present,
 * otherwise the global `DOCKER_PLATFORMS` (empty = the runner's own platform).
 */
export function platformsFor(variant, options = {}) {
  const own = (variant && variant.platforms) || [];
  if (own.length > 0) return own;
  return (options && options.platforms) || [];
}

/** `1` / `true` / `yes` / `on` — the switch convention of the repository variables. */
export function isTruthy(value) {
  return ['1', 'true', 'yes', 'on'].includes(readOptional(value).toLowerCase());
}

/**
 * The tag one variant is staged under before it is published.
 *
 * The build job pushes every variant and every architecture to a staging tag
 * instead of the final tags; a separate publish job then promotes a COMPLETE set
 * to the final tags, so no variant (and never `latest`) becomes visible until
 * all of them built. The tag carries the source sha, so re-tagging a version
 * with new code produces a different staging ref (a rebuild) rather than
 * promoting a stale artifact.
 */
export function stagingTagName({ version, shortSha = '', variant }) {
  return sanitizeTagName(`staging-${version}-${shortSha || 'nosha'}-${variant}`);
}

/** The staging refs of one build: one per configured image base (per registry). */
export function stagingRefsFor(build, state) {
  const tag = stagingTagName({ version: state.version, shortSha: state.shortSha, variant: build.variant });
  return (build.images || []).map((image) => {
    const ref = `${image.base}:${tag}`;
    return { base: image.base, registryId: image.registryId, tag, ref, tags: [ref] };
  });
}

/**
 * Parse the variant table.
 *
 *   <name>|<dockerfile>|<target>|<suffix>|<is-default>|<KEY=VALUE;…>|<platforms>
 *
 * Only the name is required; the rest defaults to `Dockerfile.<name>`, no
 * target, `-<name>` suffix, "not the default variant", no extra build args and
 * the global `DOCKER_PLATFORMS`. The literal suffix `none` means "no suffix at
 * all" (useful when DOCKER_META_TAGS already distinguishes variants).
 *
 * The seventh column is a comma separated platform list (`linux/amd64,
 * linux/arm64`): it is what makes a variant multi-architecture without touching
 * the workflow, and it is optional — a six-column row keeps working.
 *
 * The fifth column is the only thing that decides which variant owns `latest`
 * and the unsuffixed tags: exactly one row must say `true`, and a table with a
 * single variant is trivially its own default. Anything else is an error — a
 * silent fallback would move `latest` to a variant nobody chose.
 */
export function parseVariants(raw = DEFAULT_VARIANTS) {
  // One variant per line: the sixth column may itself contain `;` (build-arg
  // separator) and `,` (inside values like `GOPROXY=http://a,direct`), so this
  // list must not be split on those.
  const lines = splitLines(raw);
  if (lines.length === 0) throw new Error('DOCKER_VARIANTS 为空：至少要有一个变体');
  const variants = [];
  const seen = new Set();
  for (const line of lines) {
    const parts = line.split('|').map((part) => part.trim());
    const [name = '', dockerfile = '', target = '', suffix, flag = '', argsRaw = '', platformsRaw = ''] = parts;
    if (!VARIANT_NAME.test(name)) {
      throw new Error(`变体名非法：${name || '<empty>'}（只允许小写字母/数字/._-，且以字母或数字开头）`);
    }
    if (seen.has(name)) throw new Error(`变体名重复：${name}`);
    seen.add(name);
    variants.push({
      name,
      dockerfile: dockerfile || `Dockerfile.${name}`,
      target,
      suffix: suffix === undefined || suffix === '' ? `-${name}` : suffix === 'none' ? '' : suffix,
      isDefault: flag === 'true',
      buildArgs: parseVariantBuildArgs(argsRaw),
      platforms: parsePlatforms(platformsRaw),
    });
  }

  const flagged = variants.filter((variant) => variant.isDefault);
  const names = variants.map((variant) => variant.name).join(', ');
  if (flagged.length > 1) {
    throw new Error(`DOCKER_VARIANTS 里有多个变体被标记为默认（第五列为 true）：${flagged.map((v) => v.name).join(', ')}`);
  }
  if (flagged.length === 0 && variants.length > 1) {
    throw new Error(
      `无法确定默认变体（拥有 latest 与无后缀标签的那个）：请在 DOCKER_VARIANTS 第五列给一个变体写 true（可选：${names}）`,
    );
  }
  if (flagged.length === 0) variants[0].isDefault = true; // a lone variant is its own default
  return variants;
}

/** Keep only the variants named in `raw` (empty = all). Unknown names throw. */
export function variantSelection(variants, raw) {
  const wanted = splitList(raw);
  if (wanted.length === 0) return variants;
  const names = new Set(variants.map((variant) => variant.name));
  const unknown = wanted.filter((name) => !names.has(name));
  if (unknown.length > 0) {
    throw new Error(`未知变体：${unknown.join(', ')}（可选：${[...names].join(', ')}）`);
  }
  return variants.filter((variant) => wanted.includes(variant.name));
}

/**
 * A docker tag may contain ASCII letters, digits, `_`, `.` and `-`, may not
 * start with `.` or `-`, and is at most 128 characters. Invalid runs collapse
 * into a single `-`, exactly like metadata-action does.
 */
export function sanitizeTagName(value) {
  let name = String(value ?? '').trim().replace(/[^A-Za-z0-9_.-]+/g, '-').replace(/^[.-]+/, '');
  if (name === '') throw new Error(`无法从 ${JSON.stringify(String(value ?? ''))} 生成合法 tag`);
  if (name.length > 128) throw new Error(`tag 超过 128 字符：${name}`);
  return name;
}

/** Image bases are lowercased; a host part other than ghcr.io/docker.io is out of scope. */
export function normalizeImageBase(base) {
  let value = readOptional(base).replace(/^https?:\/\//, '').replace(/\/+$/, '').toLowerCase();
  if (value === '') throw new Error('镜像名为空');
  if (value.startsWith('docker.io/')) value = value.slice('docker.io/'.length);
  const [head] = value.split('/');
  if (head.includes('.') || head.includes(':')) {
    if (head !== 'ghcr.io') throw new Error(`只支持 ghcr.io 与 Docker Hub，收到：${base}`);
    if (!value.startsWith('ghcr.io/')) throw new Error(`ghcr.io 镜像名非法：${base}`);
  }
  if (!/^[a-z0-9][a-z0-9._/-]*$/.test(value)) throw new Error(`镜像名非法（只允许小写字母/数字/._-）：${base}`);
  return value;
}

/** Which registry an image base belongs to. */
export function imageRegistryOf(base) {
  const value = normalizeImageBase(base);
  if (value.startsWith('ghcr.io/')) return 'ghcr';
  if (value.split('/').length >= 2) return 'dockerhub';
  throw new Error(`镜像名需要 <命名空间>/<仓库> 形式：${base}`);
}

/**
 * The GHCR namespace, from `GHCR_IMAGE` when it is set and from the repository
 * owner otherwise. A GitHub account that differs from the Forgejo owner is
 * expressed by naming the whole image in `DOCKER_META_IMAGES`.
 */
export function ghcrNamespaceFrom(env = process.env) {
  const repoOwner = readOptional(env.GITHUB_REPOSITORY).split('/')[0] || '';
  const image = readOptional(env.GHCR_IMAGE).replace(/^https?:\/\//, '').replace(/\/+$/, '').toLowerCase();
  if (image) {
    const parts = image.split('/');
    return parts[0] === 'ghcr.io' ? parts[1] || '' : parts[0] || '';
  }
  return repoOwner;
}

/**
 * Which registries are configured, and as whom. `enabled` is driven purely by
 * the presence of the credentials, so a repository that only wants ghcr.io
 * simply does not set the Docker Hub variables. The login user of a GHCR push
 * is its namespace: that is the account the PAT belongs to.
 */
export function registriesFrom(env = process.env) {
  const ghcrNamespace = ghcrNamespaceFrom(env);
  const dockerhubUser = readOptional(env.DOCKERHUB_USERNAME);

  return [
    {
      ...REGISTRIES.ghcr,
      user: ghcrNamespace,
      namespace: ghcrNamespace,
      enabled: readOptional(env[REGISTRIES.ghcr.tokenVar]) !== '' && ghcrNamespace !== '',
    },
    {
      ...REGISTRIES.dockerhub,
      user: dockerhubUser,
      namespace: dockerhubUser,
      enabled: readOptional(env[REGISTRIES.dockerhub.tokenVar]) !== '' && dockerhubUser !== '',
    },
  ];
}

/**
 * The image bases to publish to, one per enabled registry, unless
 * DOCKER_META_IMAGES overrides them (which is also how a Docker Hub organisation
 * namespace or a GHCR account that differs from the repository owner is
 * expressed). Explicit entries for a disabled registry are an error —
 * publishing is impossible without credentials, and silently skipping an image
 * the repository asked for is worse than failing.
 *
 * `preview` (dry runs) derives a ghcr.io base even with no credentials at all,
 * so a repository can validate its variants, tags and Dockerfiles before any
 * token exists. A preview image is never pushed.
 */
export function imagesFrom(env = process.env, registries = registriesFrom(env), repoName = '', { preview = false } = {}) {
  const explicit = splitList(env.DOCKER_META_IMAGES);
  const enabled = new Set(registries.filter((entry) => entry.enabled).map((entry) => entry.id));

  if (explicit.length > 0) {
    return explicit.map((raw) => {
      const base = normalizeImageBase(raw);
      const id = imageRegistryOf(base);
      if (!enabled.has(id) && !preview) {
        throw new Error(`DOCKER_META_IMAGES 里的 ${raw} 需要 ${REGISTRIES[id].tokenVar}，但该凭据未配置`);
      }
      return { base, registryId: id };
    });
  }

  const images = [];
  const name = readOptional(repoName);
  if (!name) throw new Error('无法推导镜像名：GITHUB_REPOSITORY 为空，请设置 DOCKER_META_IMAGES');
  for (const entry of registries) {
    if (!entry.enabled) continue;
    if (entry.id === 'ghcr') {
      images.push({ base: normalizeImageBase(`ghcr.io/${entry.namespace}/${name}`), registryId: 'ghcr' });
    } else {
      images.push({ base: normalizeImageBase(`${entry.namespace}/${name}`), registryId: 'dockerhub' });
    }
  }
  if (images.length === 0 && preview) {
    const owner = readOptional(env.GITHUB_REPOSITORY).split('/')[0];
    if (owner) images.push({ base: normalizeImageBase(`ghcr.io/${owner}/${name}`), registryId: 'ghcr', preview: true });
  }
  return images;
}

/**
 * Build behaviour for this run. The context is always the repository root, and
 * attestations are always off: the only knobs left are the platforms, the build
 * args, and whether this run pushes.
 */
export function buildOptionsFrom(env = process.env, { dryRun = false } = {}) {
  return {
    context: '.',
    platforms: splitList(env.DOCKER_PLATFORMS),
    buildArgs: parseKeyValues(env.DOCKER_BUILD_ARGS, 'DOCKER_BUILD_ARGS'),
    dryRun: Boolean(dryRun),
    // A tag push publishes; a dry run only validates the build.
    push: !dryRun,
  };
}
