// The Cloudflare Worker of https://padmate.download/ (ADR-0018).
//
// It is routed on exactly two paths (wrangler.toml):
//   /        the download page for a browser; for anything that does not ask
//            for HTML (curl, wget, PowerShell, old scripts) the 302 to the
//            newest installer that the address has always answered
//   /latest  always the 302 to the newest installer: the permanent direct link
// /latest.yml, the installers and their .sha256 files are served by R2 itself,
// never by this Worker; a route on /* would put it in front of the
// application's updates. A request for any other path that reaches it anyway
// goes on to R2 untouched.
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
const LATEST_TTL = 60;
const STARS_TTL = 3600;
// A failed star count is remembered for a while too, so that a rate-limited
// GitHub is not asked again on every visit.
const STARS_FAIL_TTL = 600;

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
 * The language of the page: ?lang=zh|en, else the visitor's first language in
 * Accept-Language: zh* is Chinese, anything else English.
 *
 * @param {URL} url
 * @param {string | null} acceptLanguage
 * @returns {"zh" | "en"}
 */
export function pickLanguage(url, acceptLanguage) {
  const asked = url.searchParams.get("lang");
  if (asked === "zh" || asked === "en") return asked;
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
 * A cached value of the Cache API, or the one `load` makes, stored for `ttl`
 * seconds without delaying the answer. Every failure of the cache itself is
 * the same as a miss.
 *
 * @template T
 * @param {string} key
 * @param {ExecutionContext | undefined} ctx
 * @param {() => Promise<{ value: T, ttl: number }>} load
 * @returns {Promise<T>}
 */
async function cached(key, ctx, load) {
  const cache = globalThis.caches?.default;
  try {
    const hit = await cache?.match(key);
    if (hit) return JSON.parse(await hit.text());
  } catch {
    // A miss.
  }
  const { value, ttl } = await load();
  if (cache && ttl > 0) {
    const put = cache
      .put(
        key,
        new Response(JSON.stringify(value), {
          headers: {
            "content-type": "application/json",
            "cache-control": `max-age=${ttl}`,
          },
        }),
      )
      .catch(() => {});
    if (ctx?.waitUntil) ctx.waitUntil(put);
    else await put;
  }
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
 * The repository's star count, or null when GitHub does not give one.
 *
 * @param {ExecutionContext | undefined} ctx
 * @returns {Promise<number | null>}
 */
async function starCount(ctx) {
  return cached(STARS_KEY, ctx, async () => {
    let stars = null;
    try {
      const res = await fetch(GITHUB_REPO, {
        headers: {
          accept: "application/vnd.github+json",
          "user-agent": "padmate.download",
        },
        signal: AbortSignal.timeout(3000),
      });
      if (res.ok) {
        const n = (await res.json())?.stargazers_count;
        if (Number.isSafeInteger(n) && n >= 0) stars = n;
      }
    } catch {
      // Hidden, like a count GitHub does not give.
    }
    return { value: stars, ttl: stars === null ? STARS_FAIL_TTL : STARS_TTL };
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
  if (path !== "/" && path !== "/latest") return fetch(request);
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

  const page = path === "/" && wantsHtml(request);
  // / answers by Accept and, as a page, by Accept-Language; /latest by neither.
  const vary = path === "/" ? (page ? "Accept, Accept-Language" : "Accept") : null;

  if (!page) {
    const name = await newestInstaller(here, ctx);
    const res = name === null ? unavailable(vary) : redirect(here, name, vary);
    return request.method === "HEAD" ? headOnly(res) : res;
  }

  const lang = pickLanguage(url, request.headers.get("accept-language"));
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
      vary,
      "content-security-policy": `default-src 'none'; style-src ${style}; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`,
      "x-content-type-options": "nosniff",
      "referrer-policy": "strict-origin-when-cross-origin",
    },
  });
  return res;
}

export default { fetch: handle };
