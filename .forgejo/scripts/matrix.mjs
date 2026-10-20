// matrix.mjs — the variant plan the publish job consumes.
//
// The shipped workflow names no variant at all: a `plan` job runs this program
// after checkout and publishes two step outputs:
//
//   variants=alpine,trixie-slim          the ordered list the publish job walks
//   matrix={"variant":[...]}             the same list as a matrix, for parallel mode
//
// The default mode is sequential (one publish job, variants in table order)
// because the Forgejo runner accepts but ignores `strategy.max-parallel`; the
// matrix form is what the optional parallel mode uses:
//
//     strategy:
//       fail-fast: false
//       matrix: ${{ fromJSON(needs.plan.outputs.matrix) }}
//
// Either way a repository only ever edits its variant table (`.forgejo/
// variants.txt` or the `DOCKER_VARIANTS` variable) to add, remove or rename a
// variant. Validation happens here as well as in `resolve`, so a typo in the
// table fails during planning — before any job container is started for nothing.

import { appendFileSync, realpathSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { parseVariants, readOptional, variantSelection, variantTableRaw } from './config.mjs';
import { dispatchInput, readEventInputs } from './resolve.mjs';

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
 * The matrix to publish with: one job per selected variant.
 *
 * `INPUT_VARIANTS` narrows the run the same way it does for `resolve`, including
 * the "an unexpanded `${{ inputs.variants }}` means the input was not typed"
 * fallback — on a tag push Forgejo hands that literal string to the job.
 */
export function planMatrix(env = process.env, { warn = () => {}, table = '' } = {}) {
  const eventInputs = readEventInputs(env);
  const wanted = dispatchInput(env, 'VARIANTS', eventInputs, { warn });
  const variants = variantSelection(parseVariants(table || variantTableRaw(env)), wanted);
  if (variants.length === 0) {
    throw new Error('没有可构建的变体：检查 DOCKER_VARIANTS / variants.txt，以及本次 workflow_dispatch 的 variants 输入');
  }
  return { matrix: { variant: variants.map((variant) => variant.name) }, variants };
}

async function main() {
  const { matrix, variants } = planMatrix(process.env, {
    warn: (message) => process.stderr.write(`[WARN] ${message}\n`),
  });
  const json = JSON.stringify(matrix);
  const names = variants.map((variant) => variant.name).join(',');
  // One line each: $GITHUB_OUTPUT and fromJSON() both dislike embedded newlines.
  if (process.env.GITHUB_OUTPUT) {
    appendFileSync(process.env.GITHUB_OUTPUT, `variants=${names}\n`);
    appendFileSync(process.env.GITHUB_OUTPUT, `matrix=${json}\n`);
  }
  process.stdout.write(`variants=${names}\n`);
  process.stdout.write(`matrix=${json}\n`);
  process.stdout.write(`变体：${names}（${variants.length} 个，按表内顺序构建）\n`);
}

if (IS_DIRECT) {
  try {
    await main();
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exit(1);
  }
}
