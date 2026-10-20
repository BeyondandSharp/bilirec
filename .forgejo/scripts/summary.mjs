// summary.mjs — the digest table for the run.
//
// The official build-push-action writes a rich job summary; Forgejo runners may
// or may not expose $GITHUB_STEP_SUMMARY, so this writes there when it exists and
// always prints to stdout. The digests are the useful part: they are what a
// deployment pins.

import { appendFileSync, readFileSync, realpathSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { platformsFor } from './config.mjs';
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

/** The refs one result is about: staged, published, or planned. */
export function resultRefs(result) {
  if (Array.isArray(result.published) && result.published.length > 0) return result.published;
  if (Array.isArray(result.staging) && result.staging.length > 0) return result.staging;
  return (result.images || []).flatMap((image) => image.tags);
}

/** One line per staged/published image tag, with its digest. */
export function summaryLines(state) {
  const lines = [];
  for (const result of state.results || []) {
    const status = result.builder === 'imagetools'
      ? 'imagetools（提升）'
      : result.builder === 'buildx'
        ? (result.skipped ? 'buildx（复用 staging）' : 'buildx（staging）')
        : 'docker（降级）';
    const platforms = (result.platforms || []).length > 0 ? ` platforms=${result.platforms.join(',')}` : '';
    lines.push(`${result.variant} [${status}]${platforms}${result.digest ? ` ${result.digest}` : ''}`);
    for (const tag of resultRefs(result)) lines.push(`  ${tag}`);
  }
  return lines;
}

/** `alpine=linux/amd64+linux/arm64` per planned variant. */
export function platformSummary(state) {
  return (state.builds || state.variants || [])
    .map((build) => `${build.variant || build.name}=${platformsFor(build, state.options || {}).join('+') || '本机'}`)
    .join('、');
}

/** The job-summary markdown (a small table, no HTML). */
export function summaryMarkdown(state) {
  const rows = [];
  for (const result of state.results || []) {
    const digest = result.digest || '—';
    for (const ref of resultRefs(result)) rows.push(`| ${result.variant} | \`${ref}\` | \`${digest}\` |`);
  }
  const published = (state.results || []).some((result) => result.builder === 'imagetools');
  const staged = (state.results || []).some((result) => Array.isArray(result.staging) && result.staging.length > 0);
  const pushLine = state.dryRun
    ? '否（dry run）'
    : published
      ? '是（最终标签已发布）'
      : staged
        ? 'staging（最终标签由 publish 作业发布）'
        : '否';
  return [
    `## docker-build-push ${state.version}`,
    '',
    `- 仓库：${state.repo}`,
    `- tag：${state.tag}（sha ${state.sha ? state.sha.slice(0, 7) : '—'}）`,
    `- 变体：${state.variants.map((variant) => variant.name).join('、')}`,
    `- 平台：${platformSummary(state)}`,
    `- 推送：${pushLine}`,
    '',
    '| 变体 | tag | digest |',
    '| --- | --- | --- |',
    ...rows,
    '',
  ].join('\n');
}

async function main() {
  const state = JSON.parse(readFileSync(statePath(process.env), 'utf8'));
  for (const line of summaryLines(state)) process.stdout.write(`${line}\n`);
  if (process.env.GITHUB_STEP_SUMMARY) {
    appendFileSync(process.env.GITHUB_STEP_SUMMARY, `${summaryMarkdown(state)}\n`);
  }
}

if (IS_DIRECT) {
  try {
    await main();
  } catch (error) {
    process.stderr.write(`汇总失败：${error.message}\n`);
    process.exit(1);
  }
}
