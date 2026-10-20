// publish.mjs — promote the staged variants to their final tags.
//
// The build job pushes every variant and every architecture to a staging tag
// (`staging-<版本>-<sha>-<变体>`, see config.mjs) and touches no final tag. This
// program is the second half: it verifies that EVERY staged ref the release
// needs is present, and only then creates the final tags — under the variant
// suffixes and, for the default variant, the unsuffixed names including
// `latest`.
//
// It is a registry-side operation (`docker buildx imagetools create`, a manifest
// copy within the same repository, so no blob is re-uploaded and no rebuild
// happens). That is the point of the split: when publication fails — a registry
// hiccup, an expired token, a flaky network — re-running this step (or the whole
// run, whose build step then skips the variants whose staging tag already
// exists) costs seconds instead of a full multi-architecture rebuild.
//
// A missing staging ref is a hard error before anything is promoted, so a
// partially built release never gets a final tag.

import { mkdtempSync, readFileSync, writeFileSync, realpathSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { digestFromMetadata, refExists, runDocker } from './build-push.mjs';
import { stagingRefsFor } from './config.mjs';
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

/**
 * One entry per image base: the staging source and the final tags to create.
 *
 * The staging ref is computed from the same version/sha/variant the build job
 * used, so this program needs nothing from that job — which is what makes it
 * re-runnable on its own.
 */
export function publishPlan(state) {
  const plan = [];
  for (const build of state.builds || []) {
    const staging = new Map(stagingRefsFor(build, state).map((entry) => [entry.base, entry.ref]));
    for (const image of build.images || []) {
      const source = staging.get(image.base);
      if (source) {
        plan.push({ variant: build.variant, base: image.base, registryId: image.registryId, source, tags: image.tags });
      }
    }
  }
  return plan;
}

/** The staging refs this plan needs but cannot find in their registry. */
export function missingStaging(plan, { env = process.env, run = spawnSync } = {}) {
  return plan.filter((entry) => !refExists(entry.source, { env, run })).map((entry) => entry.source);
}

/** `docker buildx imagetools create -t <final…> <staging>` */
export function promotionArgv({ source, tags, metadataFile = '' }) {
  const argv = ['buildx', 'imagetools', 'create'];
  for (const tag of tags) argv.push('--tag', tag);
  if (metadataFile) argv.push('--metadata-file', metadataFile);
  argv.push(source);
  return argv;
}

async function main() {
  const env = process.env;
  const state = JSON.parse(readFileSync(statePath(env), 'utf8'));
  const plan = publishPlan(state);
  if (plan.length === 0) {
    process.stderr.write('没有可发布的镜像：请先运行 meta 步骤（检查变体表、DOCKER_META_IMAGES 与 registry 凭据）\n');
    process.exit(1);
  }
  if (state.dryRun) {
    process.stdout.write('dry run：跳过发布（构建阶段用的是 --output type=cacheonly，没有 staging 镜像）\n');
    return;
  }

  const missing = missingStaging(plan, { env, run: spawnSync });
  if (missing.length > 0) {
    process.stderr.write(
      `找不到 staging 镜像，未发布任何最终标签：\n  ${missing.join('\n  ')}\n` +
        '  staging 标签由 build 作业推送：先确认它成功（重跑 build 作业时已存在的变体会自动跳过构建），' +
        '或者 staging 标签被手工清理了 —— 用 DOCKER_FORCE_BUILD=1 可以强制重建。\n',
    );
    process.exit(1);
  }

  const workDir = mkdtempSync(join(tmpdir(), 'docker-publish-'));
  const results = new Map();
  // The step owns the results: a retried publish must not append duplicates to
  // the summary.
  state.results = [];

  for (const entry of plan) {
    const metadataFile = join(workDir, `publish-${entry.variant}-${entry.registryId}.json`);
    const result = runDocker(promotionArgv({ source: entry.source, tags: entry.tags, metadataFile }), {
      env,
      run: spawnSync,
    });
    if (result.status !== 0) {
      process.stderr.write(
        `发布失败：${entry.source} → ${entry.tags.join('、')}（docker buildx imagetools create 退出码 ${result.status}）\n` +
          '  已经创建的标签保持不变；修好网络/凭据后重跑 publish 作业即可，不需要重新构建。\n',
      );
      process.exit(1);
    }

    let digest = '';
    try {
      digest = digestFromMetadata(JSON.parse(readFileSync(metadataFile, 'utf8')));
    } catch {
      digest = '';
    }

    const current = results.get(entry.variant) || {
      variant: entry.variant,
      builder: 'imagetools',
      digest: '',
      published: [],
      images: [],
    };
    current.digest = digest || current.digest;
    current.published.push(...entry.tags);
    current.images.push({ base: entry.base, tags: entry.tags });
    results.set(entry.variant, current);

    state.results = [...results.values()];
    writeFileSync(statePath(env), JSON.stringify(state, null, 2));
    process.stdout.write(`已发布 ${entry.variant}：${entry.tags.join('、')}\n`);
  }

  process.stdout.write(`\n发布完成：${results.size} 个变体\n`);
}

if (IS_DIRECT) {
  try {
    await main();
  } catch (error) {
    process.stderr.write(`发布失败：${error.message}\n`);
    process.exit(1);
  }
}
