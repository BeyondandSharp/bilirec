// meta.mjs — the Forgejo port of docker/metadata-action.
//
// It computes the tags and OCI labels from the git ref and the repository
// variables, for two registries (ghcr.io and Docker Hub) and for every variant.
// It never talks to Docker or the network: it is pure string work plus one state
// file write, which is why the whole tagging policy is unit-testable.
//
// Implemented subset of the official `tags` input: type=semver, ref, sha, raw,
// match, with enable / priority / prefix / suffix / value / pattern / match /
// group / event / format. `flavor` supports latest / prefix / suffix / onlatest.
// Expressions: {{branch}}, {{tag}}, {{sha}} (and {{raw}}/{{version}}/{{major}}/
// {{minor}}/{{patch}} inside semver patterns). Everything else — {{date}},
// {{commit_date}}, {{is_default_branch}}, type=edge/schedule/pep440 — is
// rejected with an explicit "not implemented" error rather than silently
// producing a different tag.
//
// Variant rule (the whole point of this port):
//   * every variant gets the full tag set with its own suffix;
//   * the default variant additionally gets the unsuffixed set;
//   * `latest` exists only in that unsuffixed set, so exactly one variant owns it;
//   * each variant also gets a floating tag named after itself (on tag events).

import { readFileSync, writeFileSync, realpathSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { parseKeyValues, readOptional, sanitizeTagName } from './config.mjs';
import { statePath } from './resolve.mjs';

/** Default rules: tag, full version, major.minor, short sha. */
export const DEFAULT_TAGS = [
  'type=ref,event=tag',
  'type=semver,pattern={{version}}',
  'type=semver,pattern={{major}}.{{minor}}',
  'type=sha',
].join('\n');

export const DEFAULT_FLAVOR = 'latest=auto';

const PRIORITY = { schedule: 1000, semver: 900, pep440: 900, match: 800, edge: 700, ref: 600, raw: 200, sha: 100 };
const SUPPORTED_TYPES = ['semver', 'ref', 'sha', 'raw', 'match'];
const UNSUPPORTED_TYPES = { schedule: 'type=schedule', pep440: 'type=pep440', edge: 'type=edge' };
const GENERAL_EXPRESSIONS = ['branch', 'tag', 'sha'];
const SEMVER_EXPRESSIONS = ['raw', 'version', 'major', 'minor', 'patch'];

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

/** Every `{{…}}` token in a string, trimmed and de-duplicated. */
export function findExpressions(text) {
  const found = new Set();
  for (const match of String(text ?? '').matchAll(/\{\{\s*([^}]*?)\s*\}\}/g)) found.add(match[1]);
  return [...found];
}

export function assertSupportedExpressions(text, allowed, where) {
  const bad = findExpressions(text).filter((token) => !allowed.includes(token.split(/\s+/)[0]));
  if (bad.length > 0) {
    throw new Error(
      `${where} 使用了未实现的表达式：${bad.map((token) => `{{${token}}}`).join(', ')}` +
        `（本 Action 只实现 ${allowed.map((token) => `{{${token}}}`).join('、')}）`,
    );
  }
}

/** Replace the general expressions. */
export function renderExpression(template, ctx) {
  return String(template ?? '').replace(/\{\{\s*(branch|tag|sha)\s*\}\}/g, (_, key) => ctx[key] ?? '');
}

/** Parse the `tags` input into rules. */
export function parseTagRules(raw = DEFAULT_TAGS) {
  const lines = String(raw ?? '')
    .split(/[\n;]+/)
    .map((line) => line.trim())
    .filter((line) => line !== '' && !line.startsWith('#'));
  if (lines.length === 0) throw new Error('DOCKER_META_TAGS 为空：至少要有一条规则');

  return lines.map((line) => {
    const parts = line.split(',');
    const first = parts.shift()?.trim() || '';
    const attrs = {};
    if (first.startsWith('type=')) attrs.type = first.slice('type='.length).trim();
    else if (first.includes('=')) {
      const index = first.indexOf('=');
      attrs[first.slice(0, index).trim()] = first.slice(index + 1).trim();
    } else {
      // Shorthand for type=raw,value=<first>.
      attrs.type = 'raw';
      attrs.value = first;
    }
    for (const part of parts) {
      const index = part.indexOf('=');
      if (index <= 0) throw new Error(`DOCKER_META_TAGS 的分段需要 key=value：${part}`);
      attrs[part.slice(0, index).trim()] = part.slice(index + 1).trim();
    }
    const type = attrs.type || 'raw';
    if (UNSUPPORTED_TYPES[type]) throw new Error(`DOCKER_META_TAGS 暂未实现 ${UNSUPPORTED_TYPES[type]}（本 Action 只实现 ${SUPPORTED_TYPES.join('/')}）`);
    if (!SUPPORTED_TYPES.includes(type)) throw new Error(`未知的 tag 规则 type：${type}`);
    if (attrs.enable !== undefined && !['', 'true', 'false'].includes(attrs.enable)) {
      throw new Error(`tag 规则的 enable 只支持 true/false，收到：${attrs.enable}`);
    }
    return { type, attrs, line };
  });
}

export function parseFlavor(raw = DEFAULT_FLAVOR) {
  const flavor = { latest: 'auto', prefix: '', suffix: '', onlatest: false };
  for (const line of String(raw ?? '').split(/[\n;]+/)) {
    const item = line.trim();
    if (item === '' || item.startsWith('#')) continue;
    const index = item.indexOf('=');
    const key = (index === -1 ? item : item.slice(0, index)).trim();
    const value = index === -1 ? '' : item.slice(index + 1).trim();
    if (key === 'latest') {
      if (!['auto', 'true', 'false'].includes(value)) throw new Error(`flavor latest 只支持 auto/true/false，收到：${value}`);
      flavor.latest = value;
      continue;
    }
    if (key === 'prefix' || key === 'suffix') {
      const [text, ...flags] = value.split(',');
      flavor[key] = text.trim();
      for (const flag of flags) {
        const eq = flag.indexOf('=');
        const flagKey = (eq === -1 ? flag : flag.slice(0, eq)).trim();
        const flagValue = eq === -1 ? '' : flag.slice(eq + 1).trim();
        if (flagKey !== 'onlatest') throw new Error(`flavor ${key} 只支持 onlatest 附加项，收到：${flagKey}`);
        flavor.onlatest = flagValue === 'true';
      }
      continue;
    }
    if (key === 'onlatest') {
      flavor.onlatest = value === 'true';
      continue;
    }
    throw new Error(`未知的 flavor 项：${key}`);
  }
  return flavor;
}

/** `{{…}}` rendering inside a semver pattern. */
export function expandSemverPattern(pattern, { version, raw, isPrerelease }) {
  const text = pattern || '{{version}}';
  // Pre-releases only extend {{version}} / {{raw}}: `1.2` must never start
  // pointing at an rc, and `latest` is suppressed separately.
  if (isPrerelease && !/\{\{\s*(version|raw)\s*\}\}/.test(text)) return '';
  const [major = '', minor = '', patch = ''] = String(version).split('-')[0].split('.');
  const values = { raw: String(raw), version: String(version), major, minor, patch };
  return text.replace(/\{\{\s*(raw|version|major|minor|patch)\s*\}\}/g, (_, key) => values[key]);
}

/** The ref without its `refs/…` prefix. */
export function shortRef(ref) {
  return String(ref ?? '').replace(/^refs\/(heads|tags)\//, '');
}

/** The tag one rule contributes, before flavor and variant suffixes. */
export function ruleTagName(rule, ctx) {
  const { attrs } = rule;
  const generic = { branch: ctx.branch, tag: ctx.tag, sha: ctx.sha };

  if (rule.type === 'raw') {
    const value = renderExpression(attrs.value ?? '', generic);
    return value;
  }

  if (rule.type === 'sha') {
    // {{sha}} is the short commit; format=long needs the full one.
    const base = attrs.format === 'long' ? ctx.fullSha || ctx.sha : ctx.sha.slice(0, 7);
    const prefix = attrs.prefix === undefined ? 'sha-' : renderExpression(attrs.prefix, generic);
    return `${prefix}${base}`;
  }

  if (rule.type === 'ref') {
    const event = attrs.event || 'branch';
    if (event === 'pr') {
      if (!ctx.isPrEvent) return '';
      const number = /^refs\/pull\/(\d+)\//.exec(ctx.ref || '')?.[1] || '';
      const prefix = attrs.prefix === undefined ? 'pr-' : renderExpression(attrs.prefix, generic);
      return number ? `${prefix}${number}` : '';
    }
    if (event === 'tag') {
      if (!ctx.isTagEvent) return '';
      return renderExpression(attrs.prefix ?? '', generic) + ctx.tag + renderExpression(attrs.suffix ?? '', generic);
    }
    if (event !== 'branch') throw new Error(`tag 规则的 ref event 只支持 branch/tag/pr，收到：${event}`);
    if (!ctx.isBranchEvent) return '';
    return renderExpression(attrs.prefix ?? '', generic) + shortRef(ctx.ref) + renderExpression(attrs.suffix ?? '', generic);
  }

  if (rule.type === 'match') {
    const value = renderExpression(attrs.value ?? '', generic) || ctx.tag;
    const pattern = attrs.pattern || '';
    if (!pattern) throw new Error('type=match 需要 pattern');
    const regex = new RegExp(pattern);
    const matched = regex.exec(value);
    if (!matched) return '';
    const group = Number.parseInt(attrs.group || '0', 10);
    return matched[group] ?? '';
  }

  if (rule.type === 'semver') {
    const value = renderExpression(attrs.value ?? '', generic) || ctx.tag;
    const semverValue = String(value).replace(/^v/, '');
    const match = attrs.match ? new RegExp(attrs.match).exec(semverValue) : null;
    if (attrs.match && !match) return '';
    const version = match ? match[1] ?? semverValue : semverValue;
    if (!/^\d+\.\d+\.\d+/.test(version)) return '';
    const rendered = expandSemverPattern(attrs.pattern, {
      version,
      raw: value,
      isPrerelease: version.includes('-'),
    });
    return rendered;
  }

  return '';
}

/**
 * Turn rules into the unsuffixed tag set the official metadata-action would
 * output, flavor applied: `[{name, priority, isLatest}]`, highest priority first.
 */
export function generateTags({ rules, flavor, ctx }) {
  const collected = new Map();
  let latestFrom = null;

  const add = (name, priority, isLatest = false) => {
    const key = String(name);
    if (key === '') return;
    // Names are sanitized here, so every consumer of the base tag set already
    // holds Docker-legal tags (`releases/v1` -> `releases-v1`).
    const clean = sanitizeTagName(key);
    const existing = collected.get(clean);
    if (!existing || existing.priority <= priority) collected.set(clean, { name: clean, priority, isLatest });
  };

  for (const rule of rules) {
    if (rule.attrs.enable === 'false') continue;
    const priority = rule.attrs.priority !== undefined ? Number(rule.attrs.priority) : PRIORITY[rule.type];
    let name = ruleTagName(rule, ctx);
    if (name === '') continue;
    name = renderExpression(rule.attrs.prefix ?? '', { branch: ctx.branch, tag: ctx.tag, sha: ctx.sha }) +
      name +
      renderExpression(rule.attrs.suffix ?? '', { branch: ctx.branch, tag: ctx.tag, sha: ctx.sha });
    add(`${flavor.prefix}${name}${flavor.suffix}`, Number.isFinite(priority) ? priority : 0);

    const latestCapable = rule.type === 'semver' || rule.type === 'match' || (rule.type === 'ref' && (rule.attrs.event || 'branch') === 'tag');
    if (latestCapable && !ctx.isPrerelease) latestFrom = latestFrom === null ? priority : Math.max(latestFrom, priority);
  }

  if (flavor.latest !== 'false' && latestFrom !== null && (flavor.latest === 'true' || flavor.latest === 'auto')) {
    add(flavor.onlatest ? `${flavor.prefix}latest${flavor.suffix}` : 'latest', latestFrom, true);
  }

  return [...collected.values()].sort((a, b) => b.priority - a.priority || a.name.localeCompare(b.name));
}

/**
 * The tag list for one variant: every non-`latest` tag suffixed, plus — for the
 * default variant only — the untouched set, which is where `latest` lives.
 */
export function variantTagNames(baseTags, variant, { floating = true, isTagEvent = true } = {}) {
  const suffixed = baseTags.filter((tag) => !tag.isLatest).map((tag) => `${tag.name}${variant.suffix}`);
  const names = variant.isDefault ? [...baseTags.map((tag) => tag.name), ...suffixed] : suffixed;
  if (floating && isTagEvent) names.push(variant.name);
  return names.map((name) => sanitizeTagName(name)).filter((name, index, all) => all.indexOf(name) === index);
}

/** OCI labels, with the repository's own overrides winning. */
export function buildLabels(ctx, custom = [], now = new Date()) {
  const source = `${ctx.serverUrl}/${ctx.repo}`.replace(/^\/+/, '');
  const labels = {
    'org.opencontainers.image.created': now.toISOString(),
    'org.opencontainers.image.revision': ctx.revision ?? ctx.sha,
    'org.opencontainers.image.version': ctx.version,
    'org.opencontainers.image.source': source,
    'org.opencontainers.image.url': source,
    'org.opencontainers.image.title': ctx.repoName,
  };
  for (const { key, value } of custom) labels[key] = value;
  return labels;
}

/** One build plan entry per variant, each with the tags of every image. */
export function planBuilds({ state, rules, flavor, customLabels = [], now = new Date() }) {
  const ctx = {
    // {{branch}} / {{tag}} are empty when they do not apply, exactly like
    // metadata-action, so a tag push never yields a branch tag by accident.
    branch: state.isBranchEvent ? shortRef(state.ref) : '',
    tag: state.isTagEvent ? state.tag : '',
    sha: state.shortSha,
    fullSha: state.sha,
    ref: state.ref,
    isTagEvent: state.isTagEvent,
    isBranchEvent: state.isBranchEvent,
    isPrEvent: state.isPrEvent,
    isPrerelease: state.prerelease,
  };
  const baseTags = generateTags({ rules, flavor, ctx });
  if (baseTags.length === 0) {
    throw new Error('DOCKER_META_TAGS 在当前 ref 下没有产出任何 tag（检查 type=ref 的 event 与 tag 规则）');
  }
  const labels = buildLabels(
    {
      ...ctx,
      revision: state.sha,
      version: state.version,
      repo: state.repo,
      repoName: state.repoName,
      serverUrl: state.serverUrl,
    },
    customLabels,
    now,
  );

  return state.variants.map((variant) => {
    const tagNames = variantTagNames(baseTags, variant, { isTagEvent: state.isTagEvent });
    return {
      variant: variant.name,
      suffix: variant.suffix,
      dockerfile: variant.dockerfile,
      target: variant.target,
      isDefault: variant.isDefault,
      // Per-variant build args from the variant table (e.g. NODE_IMAGE), applied
      // by build-push.mjs on top of the globally injected ones.
      buildArgs: variant.buildArgs || [],
      // The variant's own platform list (seventh column); empty means "use the
      // global DOCKER_PLATFORMS", which build-push.mjs resolves.
      platforms: variant.platforms || [],
      tagNames,
      labels,
      images: state.images.map((image) => ({
        base: image.base,
        registryId: image.registryId,
        tags: tagNames.map((name) => `${image.base}:${name}`),
      })),
    };
  });
}

/**
 * The same tag on the same image produced by two variants means one build
 * silently overwrites the other: refuse instead of guessing.
 */
export function findDuplicateTags(builds) {
  const seen = new Map();
  const duplicates = [];
  for (const build of builds) {
    for (const name of build.tagNames) {
      const key = name;
      if (seen.has(key) && seen.get(key) !== build.variant) duplicates.push({ tag: key, variants: [seen.get(key), build.variant] });
      else seen.set(key, build.variant);
    }
  }
  return duplicates;
}

/** The whole meta step as a pure function (used by main and by the tests). */
/**
 * The workflow always exports these variables (`X: ${{ vars.X }}`), so an
 * unconfigured repository hands the programs an EMPTY STRING rather than an
 * unset variable. Empty therefore has to mean "use the default" — `??` alone
 * would let `''` through and fail every run of a repository that never set the
 * variable.
 */
export function orDefault(raw, fallback) {
  return raw === undefined || raw === null || String(raw).trim() === '' ? fallback : raw;
}

export function computeBuilds({ state, tagsRaw, flavorRaw, labelsRaw, now = new Date() }) {
  const rules = parseTagRules(orDefault(tagsRaw, DEFAULT_TAGS));
  const flavor = parseFlavor(orDefault(flavorRaw, DEFAULT_FLAVOR));
  for (const rule of rules) {
    const generic = ['branch', 'tag', 'sha'];
    if (rule.type === 'semver') {
      assertSupportedExpressions(rule.attrs.pattern ?? '', SEMVER_EXPRESSIONS, 'type=semver 的 pattern');
    }
    assertSupportedExpressions([rule.attrs.value, rule.attrs.prefix, rule.attrs.suffix].join(' '), generic, `tag 规则 ${rule.line}`);
  }
  assertSupportedExpressions([flavor.prefix, flavor.suffix].join(' '), ['branch', 'tag', 'sha'], 'DOCKER_META_FLAVOR');
  const customLabels = parseKeyValues(labelsRaw, 'DOCKER_META_LABELS');
  assertSupportedExpressions(customLabels.map((item) => item.value).join(' '), ['branch', 'tag', 'sha'], 'DOCKER_META_LABELS');

  const builds = planBuilds({ state, rules, flavor, now, customLabels });
  const duplicates = findDuplicateTags(builds);
  if (duplicates.length > 0) {
    throw new Error(
      `同一镜像上出现重复 tag（会让一个变体覆盖另一个）：${duplicates.map((item) => `${item.tag}(${item.variants.join('/')})`).join(', ')}`,
    );
  }
  return builds;
}

async function main() {
  const state = JSON.parse(readFileSync(statePath(process.env), 'utf8'));
  state.builds = computeBuilds({
    state,
    tagsRaw: process.env.DOCKER_META_TAGS,
    flavorRaw: process.env.DOCKER_META_FLAVOR,
    labelsRaw: process.env.DOCKER_META_LABELS,
  });
  writeFileSync(statePath(process.env), JSON.stringify(state, null, 2));

  for (const build of state.builds) {
    process.stdout.write(`== ${build.variant}${build.isDefault ? '（默认）' : ''} — ${build.dockerfile}${build.target ? `#${build.target}` : ''}\n`);
    for (const name of build.tagNames) process.stdout.write(`   ${name}\n`);
  }
  const total = state.builds.reduce((sum, build) => sum + build.images.reduce((count, image) => count + image.tags.length, 0), 0);
  process.stdout.write(`共 ${state.builds.length} 个变体、${total} 个 tag\n`);
}

if (IS_DIRECT) {
  try {
    await main();
  } catch (error) {
    process.stderr.write(`生成 tag/label 失败：${error.message}\n`);
    process.exit(1);
  }
}
