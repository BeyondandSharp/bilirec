// retry.mjs — bounded retries for transient network failures.
//
// A release talks to the outside world in several places: the registry token
// service (`docker login`), ghcr.io / Docker Hub (push, `imagetools inspect`
// and `imagetools create`), Docker Hub for the builder and binfmt images, and
// GitHub Releases for a swapped-in buildx binary. A flaky link there — an
// i/o timeout mid-push, a 503 from the token service, a TLS handshake timeout,
// a Docker Hub rate-limit answer — must not fail a release whose artifacts are
// fine. Every such call is retried a bounded number of times with exponential
// backoff.
//
// Two things are NOT retried, on purpose:
//
//   * authentication / permission answers (`unauthorized`, `denied`, `E401`,
//     an expired token). Waiting cannot fix them; retrying only delays the
//     actionable error and burns the job budget.
//   * "this does not exist" answers (`manifest unknown`, `404`, npm's `E404`).
//     They are a result, not a transport failure — the caller turns them into
//     "staging missing" or "version already published", which are meaningful
//     states.
//
// There is no sleep/backoff-by-timer magic: everything is injectable
// (`sleepSync`, `sleep`, `onRetry`, `classify`), so the tests drive the retry
// policy without waiting. The module is intentionally dependency-free and lives
// inside each Action directory, because every Action must stay copyable on its
// own.

/** Total attempts (the first try counts as one) when nothing is configured. */
export const DEFAULT_ATTEMPTS = 3;

/** Backoff unit in milliseconds; doubles per retry. */
export const DEFAULT_DELAY_MS = 2000;

/** Upper bound for one backoff wait (a rate-limited registry may need longer). */
export const MAX_DELAY_MS = 30_000;

/** A rate-limit answer deserves a longer wait than a plain timeout. */
export const RATE_LIMIT_DELAY_MS = 10_000;

// Checked in this order: a rate-limit answer is a 429 (also `toomanyrequests`),
// a transport failure is a timeout / connection / 5xx / EOF, and everything
// else that is recognisable is permanent (auth, missing, invalid).
const RATE_LIMIT = [
  /\btoomanyrequests\b/i,
  /\btoo many requests\b/i,
  /\brate limit\b/i,
  /\b(?:HTTP(?: status)?|status(?: code)?) 429\b/i,
  /\b429 Too Many Requests\b/i,
  /\bE429\b/,
];

const NETWORK = [
  /\bi\/o timeout\b/i,
  /\bdial tcp\b/i,
  /\bconnection (?:reset|refused|aborted|timed out)\b/i,
  /\bconnection timeout\b/i,
  /\bTLS handshake timeout\b/i,
  /\bnet\/http: request canceled\b/i,
  /\bunexpected EOF\b/i,
  /\bEOF\b/,
  /\btemporary failure in name resolution\b/i,
  /\btemporary failure resolving\b/i,
  /\bno such host\b/i,
  /\bcould not resolve (?:host|'[^']*')/i,
  /\bname or service not known\b/i,
  /\bsocket hang up\b/i,
  /\bremote end closed connection\b/i,
  /\bnetwork is unreachable\b/i,
  /\bno route to host\b/i,
  /\bECONNRESET\b/i,
  /\bECONNREFUSED\b/i,
  /\bETIMEDOUT\b/i,
  /\bESOCKETTIMEDOUT\b/i,
  /\bEAI_AGAIN\b/i,
  /\bENETUNREACH\b/i,
  /\bEHOSTUNREACH\b/i,
  /\bENOTFOUND\b/i,
  /\bEPIPE\b/i,
  /\bService Unavailable\b/i,
  /\bBad Gateway\b/i,
  /\bGateway Timeout\b/i,
  /\bInternal Server Error\b/i,
  /\b(?:HTTP(?: status)?|status(?: code)?) (?:408|425|500|502|503|504)\b/i,
  /\bfailed to connect\b/i,
  /\bcould not connect to (?:server|host)\b/i,
  /\bnetwork request to\b/i,
  /\bnetwork connectivity\b/i,
];

const PERMANENT = [
  /\bunauthori[sz]ed\b/i,
  /\bforbidden\b/i,
  /\bdenied\b/i,
  /\bauthentication required\b/i,
  /\bmanifest unknown\b/i,
  /\bname unknown\b/i,
  /\bno such (?:image|manifest|tag|reference)\b/i,
  /\b(?:tag|manifest|reference|repository|image) (?:not found|unknown)\b/i,
  /\bnot found\b/i,
  /\bunknown flag\b/i,
  /\bunknown command\b/i,
  /\binvalid reference format\b/i,
  /\bno basic auth credentials\b/i,
  /\bno space left on device\b/i,
  /\bpermission denied\b/i,
  /\bcannot connect to the docker daemon\b/i,
  /\bis the docker daemon running\b/i,
  /\bE401\b/,
  /\bE403\b/,
  /\bE404\b/,
  /\bE409\b/,
  /\bEOTP\b/,
  /\bENEEDAUTH\b/,
  /\bEPUBLISHCONFLICT\b/i,
  /\bEACCES\b/,
  /\bEPERM\b/,
];

const TAIL = 16_384;

/** The last `TAIL` characters of a log tail, so classifying stays cheap. */
function tailOf(text) {
  const value = String(text ?? '');
  return value.length > TAIL ? value.slice(-TAIL) : value;
}

/**
 * Classify the output of a failed command.
 *
 * Returns one of `'rate-limit'`, `'network'`, `'permanent'`, `'unknown'`.
 * Only the first two are worth retrying; `'unknown'` is deliberately not
 * retried, because an unrecognised failure is more likely a real error than a
 * transient one.
 */
export function classifyFailure(text) {
  const tail = tailOf(text);
  if (!tail.trim()) return 'unknown';
  if (RATE_LIMIT.some((pattern) => pattern.test(tail))) return 'rate-limit';
  if (NETWORK.some((pattern) => pattern.test(tail))) return 'network';
  if (PERMANENT.some((pattern) => pattern.test(tail))) return 'permanent';
  return 'unknown';
}

/** Is this failure a transport failure that a later attempt may survive? */
export function isRetryable(text) {
  const kind = classifyFailure(text);
  return kind === 'network' || kind === 'rate-limit';
}

/** A human-readable word for a classification, for log lines. */
export function kindLabel(kind) {
  if (kind === 'rate-limit') return '服务端限流';
  if (kind === 'network') return '网络错误';
  if (kind === 'permanent') return '不可重试的错误';
  return '未识别的错误';
}

/** `n` as a positive integer, or `fallback` when unset/invalid. */
export function positiveInt(value, fallback) {
  const number = Number(String(value ?? '').trim());
  return Number.isFinite(number) && number >= 1 ? Math.floor(number) : fallback;
}

/** The retry policy of this run, from the `DOCKER_RETRY_*` variables. */
export function retryConfig(env = process.env, { prefix = 'DOCKER' } = {}) {
  const configured = positiveInt(env[`${prefix}_RETRY_ATTEMPTS`], DEFAULT_ATTEMPTS);
  const rawDelay = String(env[`${prefix}_RETRY_DELAY`] ?? '').trim();
  const seconds = rawDelay === '' ? NaN : Number(rawDelay);
  const delayMs = Number.isFinite(seconds) && seconds >= 0 ? Math.round(seconds * 1000) : DEFAULT_DELAY_MS;
  const disabled = /^(?:1|true|yes|on)$/i.test(String(env[`${prefix}_RETRY_DISABLED`] ?? '').trim());
  return {
    attempts: disabled ? 1 : Math.min(configured, 10),
    delayMs,
    maxDelayMs: Math.max(MAX_DELAY_MS, delayMs),
  };
}

/** How long to wait after a failed attempt (1-based). 0ms means "immediately". */
export function delayFor(attempt, { delayMs = DEFAULT_DELAY_MS, maxDelayMs = MAX_DELAY_MS } = {}, kind = 'network') {
  if (delayMs <= 0) return 0;
  const base = kind === 'rate-limit' ? Math.max(RATE_LIMIT_DELAY_MS, delayMs * 5) : delayMs;
  return Math.min(maxDelayMs, base * 2 ** Math.max(0, attempt - 1));
}

/** Blocking sleep, for the synchronous (spawnSync) call sites. */
export function sleepSync(ms) {
  if (ms > 0) Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}

/**
 * Run `attemptFn` until it reports success, a permanent failure, or the attempt
 * budget is spent.
 *
 * `attemptFn()` must return `{ ok, text, ... }`: `ok` decides success and `text`
 * is the captured output that `classify` inspects on failure. The returned
 * object is the last attempt's, plus `attempts` and (on failure) `kind`.
 */
export function retrySync(attemptFn, {
  attempts = DEFAULT_ATTEMPTS,
  delayMs = DEFAULT_DELAY_MS,
  maxDelayMs = MAX_DELAY_MS,
  classify = classifyFailure,
  sleep = sleepSync,
  onRetry = () => {},
} = {}) {
  const budget = Math.max(1, Math.floor(attempts));
  let result;
  for (let attempt = 1; attempt <= budget; attempt += 1) {
    result = attemptFn(attempt);
    if (result && result.ok) return { ...result, attempts: attempt };
    const kind = classify(String((result && result.text) || ''));
    const retryable = kind === 'network' || kind === 'rate-limit';
    if (!retryable || attempt === budget) return { ...result, attempts: attempt, kind };
    const wait = delayFor(attempt, { delayMs, maxDelayMs }, kind);
    onRetry({ attempt, attempts: budget, kind, wait, result });
    sleep(wait);
  }
  return result;
}

/** Async twin of `retrySync`, for `fetch`-based call sites. */
export async function retryAsync(attemptFn, {
  attempts = DEFAULT_ATTEMPTS,
  delayMs = DEFAULT_DELAY_MS,
  maxDelayMs = MAX_DELAY_MS,
  classify = classifyFailure,
  sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
  onRetry = () => {},
} = {}) {
  const budget = Math.max(1, Math.floor(attempts));
  let result;
  for (let attempt = 1; attempt <= budget; attempt += 1) {
    result = await attemptFn(attempt);
    if (result && result.ok) return { ...result, attempts: attempt };
    const kind = classify(String((result && result.text) || ''));
    const retryable = kind === 'network' || kind === 'rate-limit';
    if (!retryable || attempt === budget) return { ...result, attempts: attempt, kind };
    const wait = delayFor(attempt, { delayMs, maxDelayMs }, kind);
    onRetry({ attempt, attempts: budget, kind, wait, result });
    await sleep(wait);
  }
  return result;
}

/** The retry log line, so every call site reports the same way. */
export function retryLine(label, { attempt, attempts, kind, wait }) {
  return `[WARN] ${label}失败（${kindLabel(kind)}）：第 ${attempt}/${attempts} 次尝试，` +
    `${wait > 0 ? `${Math.round(wait / 1000)}s 后` : '立即'}重试`;
}
