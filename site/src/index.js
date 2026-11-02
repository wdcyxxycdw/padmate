// The Cloudflare Worker of https://padmate.download/ (ADR-0018).
//
// It answers four paths (wrangler.toml routes them here):
//   /        the download page for a browser, in the visitor's language; for
//            anything that does not ask for HTML (curl, wget, PowerShell, old
//            scripts) the 302 to the newest installer that the address has
//            always answered
//   /zh /en  the download page in that language, for anyone; the page's
//            language link goes to the other one
//   /latest  always the 302 to the newest installer: the permanent direct link
// A route matches the whole URL, query string included, and may not contain
// one, so / and /latest are matched only without a query; /zh and /en are
// routed with a trailing wildcard so that a link with a query (WeChat adds
// ?from=…) still gets the page. /latest.yml, the installers and their .sha256
// files are served by R2 itself, never by this Worker; a route on /* would put
// it in front of the application's updates. A request for any other path that
// reaches it anyway (/zhx, /en/…) goes on to R2 untouched.
//
// The newest installer is the one /latest.yml names, the file the installed
// application's update reads too. The redirects are 302 and not cached: a 301
// would pin an old installer in browsers. app/scripts/publish-r2.mjs puts the
// release on the site and reads the 302 of / back.

import { renderPage } from "./page.js";
import { CSS } from "./style.js";

/** The installers of padmate; latest.yml is trusted for nothing else. */
const INSTALLER =
  /^padmate-v(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)-windows-amd64-setup\.exe$/;

/** Where the GitHub card's star count comes from. */
const GITHUB_REPO = "https://api.github.com/repos/wdcyxxycdw/padmate";

// Only functions and handler objects may be named exports of the main module:
// workerd takes each one as an entrypoint and refuses a string.

// The Cache API is keyed by URL. These keys are paths no route sends here and
// R2 does not have, and what they hold is public anyway.
const LATEST_KEY = "https://padmate.download/_worker-cache/latest-installer";
const STARS_KEY = "https://padmate.download/_worker-cache/github-stars";
const STARS_LAST_KEY =
  "https://padmate.download/_worker-cache/github-stars-last";
const LATEST_TTL = 60;
const STARS_TTL = 3600;
// A failed ask is remembered for a while too, so that a rate-limited GitHub is
// not asked again on every visit. Unauthenticated requests are limited per IP
// address, and a Worker shares its addresses with every other Worker.
const STARS_FAIL_TTL = 600;
// The last count GitHub gave stands in while it gives none, for up to a week:
// an older count is still a true one, unlike a guess.
const STARS_LAST_TTL = 7 * 24 * 3600;

/** The pages that have a path of their own. */
const PAGES = { "/zh": "zh", "/en": "en" };

const UNAVAILABLE =
  "padmate: the download is not available right now. Please try again later.";

/**
 * The installer the text of latest.yml names in its `path:` line, or null when
 * there is none or it is not the name of a padmate installer.
 *
 * @param {string} text
 * @returns {string | null}
 */
export function installerFromLatest(text) {
  const name = /^path:\s*(\S+)\s*$/m.exec(text)?.[1];
  return name !== undefined && INSTALLER.test(name) ? name : null;
}

/**
 * The language of the page on /: the visitor's first language in
 * Accept-Language, zh* is Chinese, anything else English.
 *
 * @param {string | null} acceptLanguage
 * @returns {"zh" | "en"}
 */
export function pickLanguage(acceptLanguage) {
  let best = null;
  let bestQ = 0;
  for (const part of (acceptLanguage ?? "").split(",")) {
    const [range, ...params] = part.trim().split(";");
    if (!range || range === "*") continue;
    let q = 1;
    for (const p of params) {
      const m = /^\s*q\s*=\s*([0-9.]+)\s*$/i.exec(p);
      if (m) q = Number(m[1]);
    }
    // The first of equal weights wins, as the header lists them by preference.
    if (q > bestQ) {
      best = range.toLowerCase();
      bestQ = q;
    }
  }
  return best !== null && (best === "zh" || best.startsWith("zh-"))
    ? "zh"
    : "en";
}

/** Whether a request asks for a web page rather than a file. */
function wantsHtml(request) {
  return /\btext\/html\b/i.test(request.headers.get("accept") ?? "");
}

/**
 * The value the Cache API holds under `key`, or undefined. Every failure of
 * the cache itself is the same as a miss.
 *
 * @param {string} key
 * @returns {Promise<unknown>}
 */
async function lookup(key) {
  try {
    const hit = await globalThis.caches?.default?.match(key);
    if (hit) return JSON.parse(await hit.text());
  } catch {
    // A miss.
  }
  return undefined;
}

/**
 * Stores `value` under `key` for `ttl` seconds, without delaying the answer
 * when the runtime can finish it later. A failure of the cache is ignored.
 *
 * @param {string} key
 * @param {unknown} value
 * @param {number} ttl
 * @param {ExecutionContext | undefined} ctx
 */
async function store(key, value, ttl, ctx) {
  const cache = globalThis.caches?.default;
  if (!cache || ttl <= 0) return;
  const put = Promise.resolve()
    .then(() =>
      cache.put(
        key,
        new Response(JSON.stringify(value), {
          headers: {
            "content-type": "application/json",
            "cache-control": `max-age=${ttl}`,
          },
        }),
      ),
    )
    .catch(() => {});
  if (ctx?.waitUntil) ctx.waitUntil(put);
  else await put;
}

/**
 * A cached value, or the one `load` makes, stored for the `ttl` it gives.
 *
 * @template T
 * @param {string} key
 * @param {ExecutionContext | undefined} ctx
 * @param {() => Promise<{ value: T, ttl: number }>} load
 * @returns {Promise<T>}
 */
async function cached(key, ctx, load) {
  const hit = await lookup(key);
  if (hit !== undefined) return /** @type {T} */ (hit);
  const { value, ttl } = await load();
  await store(key, value, ttl, ctx);
  return value;
}

/**
 * The newest installer's file name, or null when latest.yml cannot be read or
 * names nothing that is a padmate installer. Only a good answer is cached.
 *
 * @param {URL} here the site, https
 * @param {ExecutionContext | undefined} ctx
 * @returns {Promise<string | null>}
 */
async function newestInstaller(here, ctx) {
  return cached(LATEST_KEY, ctx, async () => {
    let name = null;
    try {
      const res = await fetch(new URL("/latest.yml", here), {
        cf: { cacheTtl: LATEST_TTL },
      });
      if (res.ok) name = installerFromLatest(await res.text());
    } catch {
      // The same answer as a latest.yml that names nothing.
    }
    return { value: name, ttl: name === null ? 0 : LATEST_TTL };
  });
}

/**
 * The star count GitHub gives now, or null. A failure is logged, so that
 * `wrangler tail` shows why the count is old or hidden.
 *
 * @returns {Promise<number | null>}
 */
async function askGitHub() {
  try {
    const res = await fetch(GITHUB_REPO, {
      headers: {
        accept: "application/vnd.github+json",
        "user-agent": "padmate.download",
      },
      signal: AbortSignal.timeout(3000),
    });
    if (!res.ok) {
      console.warn(
        `GitHub star count: HTTP ${res.status}, x-ratelimit-remaining ${res.headers.get("x-ratelimit-remaining")}, x-ratelimit-reset ${res.headers.get("x-ratelimit-reset")}`,
      );
      return null;
    }
    const n = (await res.json())?.stargazers_count;
    if (Number.isSafeInteger(n) && n >= 0) return n;
    console.warn("GitHub star count: no stargazers_count in the answer");
  } catch (error) {
    console.warn(`GitHub star count: ${error}`);
  }
  return null;
}

/**
 * The repository's star count: GitHub's, at most an hour old; while GitHub
 * gives none, the last count it gave, at most a week old; else null, and the
 * page hides the count.
 *
 * @param {ExecutionContext | undefined} ctx
 * @returns {Promise<number | null>}
 */
async function starCount(ctx) {
  return cached(STARS_KEY, ctx, async () => {
    const stars = await askGitHub();
    if (stars !== null) {
      await store(STARS_LAST_KEY, stars, STARS_LAST_TTL, ctx);
      return { value: stars, ttl: STARS_TTL };
    }
    const last = await lookup(STARS_LAST_KEY);
    return {
      value: Number.isSafeInteger(last) && last >= 0 ? last : null,
      ttl: STARS_FAIL_TTL,
    };
  });
}

let styleHash;
/** The CSP source of the page's one <style> element. */
function styleSource() {
  styleHash ??= crypto.subtle
    .digest("SHA-256", new TextEncoder().encode(CSS))
    .then((d) => `'sha256-${btoa(String.fromCharCode(...new Uint8Array(d)))}'`);
  return styleHash;
}

const unavailable = (vary) =>
  new Response(UNAVAILABLE, {
    status: 503,
    headers: {
      "content-type": "text/plain; charset=utf-8",
      "cache-control": "no-store",
      ...(vary ? { vary } : {}),
    },
  });

const redirect = (here, name, vary) =>
  new Response(null, {
    status: 302,
    headers: {
      location: new URL(`/${name}`, here).href,
      "cache-control": "no-cache",
      ...(vary ? { vary } : {}),
    },
  });

/** A response without its body, for HEAD. */
const headOnly = (res) =>
  new Response(null, { status: res.status, headers: res.headers });

/**
 * The answer to one request.
 *
 * @param {Request} request
 * @param {unknown} [env]
 * @param {ExecutionContext} [ctx]
 * @returns {Promise<Response>}
 */
export async function handle(request, env, ctx) {
  const url = new URL(request.url);
  const path = url.pathname;
  const pageLang = Object.hasOwn(PAGES, path) ? PAGES[path] : undefined;
  if (path !== "/" && path !== "/latest" && pageLang === undefined)
    return fetch(request);
  if (request.method !== "GET" && request.method !== "HEAD")
    return new Response("Method Not Allowed", {
      status: 405,
      headers: {
        allow: "GET, HEAD",
        "content-type": "text/plain; charset=utf-8",
      },
    });

  // Always https, also for a visitor who came over http: Chrome warns about,
  // and may block, a download that is not over https.
  const here = new URL(url);
  here.protocol = "https:";
  here.search = "";

  const page = pageLang !== undefined || (path === "/" && wantsHtml(request));
  // / answers by Accept and, as a page, by Accept-Language; the other paths by
  // neither.
  const vary =
    path === "/" ? (page ? "Accept, Accept-Language" : "Accept") : null;

  if (!page) {
    const name = await newestInstaller(here, ctx);
    const res = name === null ? unavailable(vary) : redirect(here, name, vary);
    return request.method === "HEAD" ? headOnly(res) : res;
  }

  const lang = pageLang ?? pickLanguage(request.headers.get("accept-language"));
  const [name, stars, style] = await Promise.all([
    newestInstaller(here, ctx),
    starCount(ctx),
    styleSource(),
  ]);
  if (name === null) {
    const res = unavailable(vary);
    return request.method === "HEAD" ? headOnly(res) : res;
  }
  const version = INSTALLER.exec(name)[1];
  const html = renderPage({ lang, installer: name, version, stars });
  const res = new Response(request.method === "HEAD" ? null : html, {
    status: 200,
    headers: {
      "content-type": "text/html; charset=utf-8",
      "content-language": lang === "zh" ? "zh-CN" : "en",
      "cache-control": "public, max-age=60",
      ...(vary ? { vary } : {}),
      "content-security-policy": `default-src 'none'; style-src ${style}; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`,
      "x-content-type-options": "nosniff",
      "referrer-policy": "strict-origin-when-cross-origin",
    },
  });
  return res;
}

export default { fetch: handle };
