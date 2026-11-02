// Tests of the Worker of https://padmate.download/ (site/src/index.js).
//
// Only node:test and node:assert, so that `node --test "test/*.test.js"` runs on a clean
// checkout without node_modules. The Worker is driven through handle() with a
// mocked global fetch and a mocked Cache API; nothing here touches the network.

import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { afterEach, beforeEach, describe, it } from "node:test";
import { handle, installerFromLatest, pickLanguage } from "../src/index.js";
import * as worker from "../src/index.js";
import { formatStars } from "../src/page.js";
import { QR_CODES } from "../src/qr-codes.js";

const name = "padmate-v1.1.0-windows-amd64-setup.exe";
const latest = `version: 1.1.0\nfiles:\n  - url: ${name}\n    sha512: AAAA\npath: ${name}\nsha512: AAAA\nreleaseDate: '2026-10-31T01:02:03.456Z'\n`;
const UNAVAILABLE =
  "padmate: the download is not available right now. Please try again later.";
const GITHUB_REPO = "https://api.github.com/repos/wdcyxxycdw/padmate";
const LATEST_URL = "https://padmate.download/latest.yml";
const BROWSER_ACCEPT =
  "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8";

// ---- Mocks of the platform ------------------------------------------------

const realFetch = globalThis.fetch;
const realCaches = Object.getOwnPropertyDescriptor(globalThis, "caches");

/** What the mocked world answers; each test may replace any of these. */
let world;
/** Every call of the mocked fetch: { url, input, init }. */
let fetchCalls;
/** The in-memory Cache API: key -> { body, headers }. */
let cacheStore;
/** The promises passed to ctx.waitUntil. */
let waited;

function requestUrl(input) {
  return input instanceof Request ? input.url : String(input);
}

function installCache() {
  cacheStore = new Map();
  globalThis.caches = {
    default: {
      async match(key) {
        const hit = cacheStore.get(String(key));
        return hit ? new Response(hit.body, { headers: hit.headers }) : undefined;
      },
      async put(key, response) {
        cacheStore.set(String(key), {
          body: await response.text(),
          headers: [...response.headers],
        });
      },
    },
  };
}

beforeEach(() => {
  world = {
    latest: () => new Response(latest),
    github: () => Response.json({ stargazers_count: 91 }),
    other: () => new Response("from the origin"),
  };
  fetchCalls = [];
  waited = [];
  globalThis.fetch = async (input, init) => {
    const url = requestUrl(input);
    fetchCalls.push({ url, input, init });
    if (url === LATEST_URL) return world.latest();
    if (url === GITHUB_REPO) return world.github();
    return world.other(input, init);
  };
  installCache();
});

afterEach(() => {
  globalThis.fetch = realFetch;
  if (realCaches) Object.defineProperty(globalThis, "caches", realCaches);
  else delete globalThis.caches;
});

/** A ctx whose waitUntil collects the promises, as the runtime would keep them. */
const makeCtx = () => ({ waitUntil: (p) => waited.push(p) });

/** Runs one request through the Worker and lets its background work finish. */
async function run(url, init = {}) {
  const request = new Request(url, init);
  const res = await handle(request, {}, makeCtx());
  await Promise.all(waited);
  return res;
}

const page = (path = "/", headers = {}) =>
  run(`https://padmate.download${path}`, {
    headers: { accept: BROWSER_ACCEPT, ...headers },
  });

const callsTo = (url) => fetchCalls.filter((c) => c.url === url);

/** The GitHub card of a page: from its link to the closing </a>. */
function githubCard(html) {
  const start = html.indexOf('<a class="site-card" href="https://github.com');
  assert.notEqual(start, -1, "the GitHub card is on the page");
  return html.slice(start, html.indexOf("</a>", start));
}

// ---- installerFromLatest --------------------------------------------------

describe("installerFromLatest", () => {
  it("reads the installer from the path line of latest.yml", () => {
    assert.equal(installerFromLatest(latest), name);
    assert.equal(installerFromLatest(latest.replaceAll("\n", "\r\n")), name);
    assert.equal(installerFromLatest(`path: ${name}   \n`), name);
  });

  it("reads a prerelease name too: the redirect follows what latest.yml says", () => {
    assert.equal(
      installerFromLatest("path: padmate-v1.0.3-rc.1-windows-amd64-setup.exe\n"),
      "padmate-v1.0.3-rc.1-windows-amd64-setup.exe",
    );
  });

  it("takes only the name of an installer of padmate", () => {
    for (const text of [
      "",
      "version: 1.0.3\n",
      "path:\n",
      "path: setup.exe\n",
      `path: ../${name}\n`,
      `path: /${name}\n`,
      `path: https://example.com/${name}\n`,
      `path: ${name}.sha256\n`,
      `path: x${name}\n`,
      "path: padmate-v1.0-windows-amd64-setup.exe\n",
      "path: padmate-v1.0.3-windows-arm64-setup.exe\n",
      `path: "${name}"\n`,
      `path: ${name}?x=1\n`,
      `path: ${name} extra\n`,
      // Only a line that starts with path: counts, not an entry of the list.
      `files:\n  - path: ${name}\n`,
    ])
      assert.equal(installerFromLatest(text), null, JSON.stringify(text));
  });

  it("lets the page show the version of the installer, a prerelease too", async () => {
    // The pattern's capture group is what the page prints after the v.
    world.latest = () =>
      new Response("path: padmate-v1.0.3-rc.1-windows-amd64-setup.exe\n");
    const html = await (await page("/")).text();
    assert.match(html, /<span class="ver">v1\.0\.3-rc\.1<\/span>/);
  });
});

// ---- Exports --------------------------------------------------------------

describe("the exports of src/index.js", () => {
  // workerd refuses a main module that exports anything but functions (and the
  // default object); wrangler dev fails on it, so this must not come back.
  it("are functions, except default, which is an object with a fetch function", () => {
    for (const [key, value] of Object.entries(worker)) {
      if (key === "default") {
        assert.equal(typeof value, "object");
        assert.equal(typeof value.fetch, "function");
      } else assert.equal(typeof value, "function", `export ${key}`);
    }
    assert.ok("default" in worker);
  });
});

// ---- pickLanguage ---------------------------------------------------------

describe("pickLanguage", () => {
  const at = (query, header) =>
    pickLanguage(new URL(`https://padmate.download/${query}`), header);

  it("lets ?lang win over the header", () => {
    assert.equal(at("?lang=zh", "en-US,en;q=0.9"), "zh");
    assert.equal(at("?lang=en", "zh-CN,zh;q=0.9"), "en");
  });

  it("falls back to the header for an invalid ?lang", () => {
    assert.equal(at("?lang=fr", "zh-CN"), "zh");
    assert.equal(at("?lang=fr", "en-US"), "en");
    assert.equal(at("?lang=", "zh"), "zh");
  });

  for (const [header, want] of [
    ["zh-CN,zh;q=0.9", "zh"],
    ["zh-TW", "zh"],
    ["zh", "zh"],
    ["ZH-cn", "zh"],
    ["en-US,en;q=0.9,zh-CN;q=0.8", "en"],
    ["zh-CN;q=0.5,en;q=0.9", "en"],
    ["ja", "en"],
    ["", "en"],
    [null, "en"],
    ["*", "en"],
    // A language that merely starts with the letters zh is not Chinese.
    ["zhx", "en"],
  ])
    it(`reads Accept-Language ${JSON.stringify(header)} as ${want}`, () => {
      assert.equal(at("", header), want);
    });
});

// ---- formatStars ----------------------------------------------------------

describe("formatStars", () => {
  it("writes counts as GitHub does", () => {
    for (const [n, want] of [
      [0, "0"],
      [91, "91"],
      [999, "999"],
      [1000, "1k"],
      [1234, "1.2k"],
      [12345, "12k"],
    ])
      assert.equal(formatStars(n), want);
  });
});

// ---- The page -------------------------------------------------------------

describe("the page on /", () => {
  it("answers a browser with the Chinese page", async () => {
    const res = await page("/", { "accept-language": "zh-CN,zh;q=0.9" });
    assert.equal(res.status, 200);
    assert.equal(res.headers.get("content-type"), "text/html; charset=utf-8");
    const vary = res.headers.get("vary");
    assert.match(vary, /Accept/);
    assert.match(vary, /Accept-Language/);
    const html = await res.text();
    assert.match(html, /<html lang="zh-CN">/);
    assert.match(html, /<title>Padmate 下载<\/title>/);
    assert.ok(html.includes(`href="/${name}"`));
    assert.match(html, /<span class="ver">v1\.1\.0<\/span>/);
  });

  it("answers a browser with the English page", async () => {
    const res = await page("/", { "accept-language": "en-US,en;q=0.9" });
    assert.equal(res.status, 200);
    const html = await res.text();
    assert.match(html, /<html lang="en">/);
    assert.match(html, /<title>Download Padmate<\/title>/);
    assert.ok(html.includes(`href="/${name}"`));
    assert.match(html, /<span class="ver">v1\.1\.0<\/span>/);
  });

  it("defaults to English without Accept-Language, and ?lang overrides", async () => {
    assert.match(await (await page("/")).text(), /<html lang="en">/);
    assert.match(
      await (await page("/?lang=zh", { "accept-language": "en" })).text(),
      /<html lang="zh-CN">/,
    );
  });

  it("shows the donate card and the GitHub text in Chinese only", async () => {
    const zh = await (await page("/?lang=zh")).text();
    assert.ok(zh.includes('<details class="site-card site-donate">'));
    assert.match(zh, /<summary>[\s\S]*<\/summary>/);
    assert.ok(zh.includes("微信 · 支付宝"));
    assert.ok(zh.includes("<figcaption>微信</figcaption>"));
    assert.ok(zh.includes("<figcaption>支付宝</figcaption>"));
    assert.ok(zh.includes("Padmate 免费开源，有能力的话可以点个star~"));
    assert.ok(zh.includes('<footer class="site-foot">'));

    // The stylesheet mentions <details> in a comment, so look at the markup only.
    const en = (await (await page("/?lang=en")).text()).replace(
      /<style>[\s\S]*?<\/style>/,
      "",
    );
    assert.ok(!en.includes("<details"));
    assert.ok(!en.includes("打赏"));
    assert.ok(en.includes('<footer class="site-foot is-single">'));
  });

  it("links to the other language", async () => {
    const zh = await (await page("/?lang=zh")).text();
    assert.ok(zh.includes('class="site-lang" href="?lang=en"'));
    const en = await (await page("/?lang=en")).text();
    assert.ok(en.includes('class="site-lang" href="?lang=zh"'));
  });

  it("shows each generated payment code in its box, or the design's placeholder", async () => {
    const zh = await (await page("/?lang=zh")).text();
    for (const code of [QR_CODES.wechat, QR_CODES.alipay])
      assert.ok(
        zh.includes(`<div class="code">${code ?? "收款码"}</div>`),
        String(code).slice(0, 40),
      );
    // A generated code is an inline SVG or a data: image, which the CSP allows.
    for (const code of [QR_CODES.wechat, QR_CODES.alipay].filter(Boolean))
      assert.match(code, /^(<svg [^]*<\/svg>|<img src="data:image\/(png|jpeg);base64,[^"]+" alt="[^"]+">)$/);
  });

  it("sends the security headers", async () => {
    const res = await page("/?lang=zh");
    const html = await res.text();
    const csp = res.headers.get("content-security-policy");
    assert.match(csp, /default-src 'none'/);
    assert.match(csp, /img-src data:/);
    assert.ok(!csp.includes("script-src"));
    assert.ok(!csp.includes("unsafe-inline"));

    // The policy names the hash of the exact text of the one <style> element.
    const styles = [...html.matchAll(/<style>([\s\S]*?)<\/style>/g)];
    assert.equal(styles.length, 1);
    const hash = createHash("sha256").update(styles[0][1]).digest("base64");
    assert.match(csp, new RegExp(`style-src 'sha256-${hash}'`.replace(/[+/]/g, "\\$&")));

    assert.ok(!/\sstyle\s*=/i.test(html), "no style attribute");
    assert.ok(!/<script/i.test(html), "no script");

    assert.equal(res.headers.get("x-content-type-options"), "nosniff");
    assert.ok(res.headers.get("referrer-policy"));
    const maxAge = /max-age=(\d+)/.exec(res.headers.get("cache-control"))?.[1];
    assert.ok(maxAge !== undefined && Number(maxAge) <= 300);
  });

  it("asks the visitor's browser for nothing outside the site", async () => {
    for (const lang of ["zh", "en"]) {
      const html = await (await page(`/?lang=${lang}`)).text();
      const body = html.replace(/<style>[\s\S]*?<\/style>/, "");
      const urls = [...body.matchAll(/\b(?:href|src)="([^"]*)"/g)].map((m) => m[1]);
      assert.ok(urls.length > 3);
      for (const u of urls)
        assert.ok(
          (u.startsWith("/") && !u.startsWith("//")) ||
            u.startsWith("?") ||
            u.startsWith("data:") ||
            u.startsWith("https://github.com/wdcyxxycdw/padmate"),
          `${lang}: ${u}`,
        );
      assert.ok(!/@import|url\(\s*["']?https?:/i.test(html));
    }
  });
});

// ---- The redirects --------------------------------------------------------

describe("the redirect", () => {
  const installerUrl = `https://padmate.download/${name}`;

  for (const [label, headers] of [
    ["no Accept", {}],
    ["Accept: */*", { accept: "*/*" }],
    ["Accept: application/octet-stream", { accept: "application/octet-stream" }],
    ["a PowerShell-like Accept", { accept: "*/*", "user-agent": "WindowsPowerShell/5.1" }],
  ])
    it(`is the answer of / to ${label}`, async () => {
      const res = await run("https://padmate.download/", { headers });
      assert.equal(res.status, 302);
      assert.equal(res.headers.get("location"), installerUrl);
      assert.equal(res.headers.get("cache-control"), "no-cache");
      assert.equal(res.headers.get("vary"), "Accept");
      assert.equal(res.body, null);
      assert.equal(await res.text(), "");
      // A redirect does not need the star count.
      assert.equal(callsTo(GITHUB_REPO).length, 0);
    });

  it("sends a visitor who came over http to the installer over https", async () => {
    const res = await run("http://padmate.download/");
    assert.equal(res.status, 302);
    assert.equal(res.headers.get("location"), installerUrl);
    assert.deepEqual(
      fetchCalls.map((c) => c.url),
      [LATEST_URL],
    );
  });

  it("asks for latest.yml on the same host, from the edge cache for a minute", async () => {
    await run("https://padmate.download/");
    assert.equal(fetchCalls.length, 1);
    assert.deepEqual(fetchCalls[0].init, { cf: { cacheTtl: 60 } });
  });

  it("is /latest, for a browser too", async () => {
    const res = await run("https://padmate.download/latest", {
      headers: { accept: BROWSER_ACCEPT },
    });
    assert.equal(res.status, 302);
    assert.equal(res.headers.get("location"), installerUrl);
    assert.equal(res.headers.get("cache-control"), "no-cache");
    assert.equal(res.headers.get("vary"), null);
    assert.equal(callsTo(GITHUB_REPO).length, 0);
  });

  it("ignores the query string of /latest and /", async () => {
    for (const url of [
      "https://padmate.download/latest?x=1&lang=zh",
      "http://padmate.download/latest?a=b",
      "https://padmate.download/?lang=zh",
    ]) {
      fetchCalls.length = 0;
      cacheStore.clear();
      const res = await run(url);
      assert.equal(res.status, 302, url);
      assert.equal(res.headers.get("location"), installerUrl, url);
      assert.equal(fetchCalls[0].url, LATEST_URL, url);
    }
  });
});

// ---- Failures of latest.yml -----------------------------------------------

describe("when the newest installer cannot be found", () => {
  const failures = [
    ["latest.yml is 404", () => new Response("not found", { status: 404 })],
    [
      "fetching latest.yml throws",
      () => {
        throw new Error("connection reset");
      },
    ],
    ["latest.yml names a bad file", () => new Response("path: ../evil.exe\n")],
    ["latest.yml is empty", () => new Response("")],
  ];
  const requests = [
    ["the page on /", "https://padmate.download/", { accept: BROWSER_ACCEPT }],
    ["/ for a non-browser", "https://padmate.download/", { accept: "*/*" }],
    ["/latest", "https://padmate.download/latest", {}],
  ];

  for (const [why, latestAnswer] of failures)
    for (const [what, url, headers] of requests)
      it(`answers 503 on ${what} when ${why}`, async () => {
        world.latest = latestAnswer;
        const res = await run(url, { headers });
        assert.equal(res.status, 503);
        assert.match(res.headers.get("content-type"), /^text\/plain/);
        assert.equal(res.headers.get("cache-control"), "no-store");
        assert.equal(res.headers.get("location"), null);
        assert.equal(await res.text(), UNAVAILABLE);
      });

  it("does not cache a failed latest.yml", async () => {
    world.latest = () => new Response("oops", { status: 500 });
    assert.equal((await run("https://padmate.download/latest")).status, 503);
    assert.equal(callsTo(LATEST_URL).length, 1);

    world.latest = () => new Response(latest);
    const res = await run("https://padmate.download/latest");
    assert.equal(res.status, 302);
    assert.equal(callsTo(LATEST_URL).length, 2);
  });

  it("caches a good latest.yml", async () => {
    await run("https://padmate.download/latest");
    await run("https://padmate.download/latest");
    await run("https://padmate.download/", { headers: { accept: "*/*" } });
    await page("/");
    assert.equal(callsTo(LATEST_URL).length, 1);
  });
});

// ---- The star count -------------------------------------------------------

describe("the star count", () => {
  it("shows what GitHub answers", async () => {
    const html = await (await page("/?lang=zh")).text();
    assert.ok(githubCard(html).includes('<span class="mono">91</span>'));
    assert.ok(html.includes('title="91 颗星"'));
    const en = await (await page("/?lang=en")).text();
    assert.ok(en.includes('title="91 stars"'));
  });

  it("asks GitHub for the repository with a user agent", async () => {
    await page("/");
    const calls = callsTo(GITHUB_REPO);
    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, "https://api.github.com/repos/wdcyxxycdw/padmate");
    assert.ok(new Headers(calls[0].init.headers).get("user-agent"));
  });

  for (const [why, github] of [
    ["GitHub answers 403", () => new Response("rate limited", { status: 403 })],
    ["GitHub answers 500", () => new Response("oops", { status: 500 })],
    [
      "the request throws",
      () => {
        throw new Error("network down");
      },
    ],
    ["the body is not JSON", () => new Response("<html>nope</html>")],
    ["the count is not an integer", () => Response.json({ stargazers_count: 1.5 })],
    ["the count is a string", () => Response.json({ stargazers_count: "91" })],
    ["the count is negative", () => Response.json({ stargazers_count: -1 })],
    ["there is no count", () => Response.json({})],
    ["the JSON is null", () => Response.json(null)],
  ])
    it(`hides the count and still serves the page when ${why}`, async () => {
      world.github = github;
      const res = await page("/?lang=zh");
      assert.equal(res.status, 200);
      const html = await res.text();
      const card = githubCard(html);
      assert.ok(!card.includes('class="mono"'));
      assert.ok(card.includes("Go"));
      assert.ok(card.includes("MIT"));
      assert.ok(html.includes(`href="/${name}"`));
    });

  it("caches the count", async () => {
    await page("/");
    await page("/?lang=zh");
    assert.equal(callsTo(GITHUB_REPO).length, 1);
  });

  it("remembers a failed count for a while, so GitHub is not asked on every visit", async () => {
    world.github = () => new Response("rate limited", { status: 403 });
    await page("/");
    await page("/");
    assert.equal(callsTo(GITHUB_REPO).length, 1);
  });
});

// ---- Methods --------------------------------------------------------------

describe("HEAD", () => {
  const same = async (url, headers) => {
    const get = await run(url, { headers });
    const head = await run(url, { method: "HEAD", headers });
    assert.equal(head.status, get.status, url);
    assert.deepEqual(
      [...head.headers].sort(),
      [...get.headers].sort(),
      url,
    );
    assert.equal(await head.text(), "");
    return { get, head };
  };

  it("answers the page like GET without a body", async () => {
    const { head } = await same("https://padmate.download/", {
      accept: BROWSER_ACCEPT,
    });
    assert.equal(head.status, 200);
  });

  it("answers the redirect of / like GET without a body", async () => {
    const { head } = await same("https://padmate.download/", { accept: "*/*" });
    assert.equal(head.status, 302);
  });

  it("answers /latest like GET without a body", async () => {
    const { head } = await same("https://padmate.download/latest", {});
    assert.equal(head.status, 302);
  });

  it("answers the 503 like GET without a body", async () => {
    world.latest = () => new Response("", { status: 404 });
    for (const [url, headers] of [
      ["https://padmate.download/", { accept: BROWSER_ACCEPT }],
      ["https://padmate.download/", {}],
      ["https://padmate.download/latest", {}],
    ]) {
      const { head } = await same(url, headers);
      assert.equal(head.status, 503);
    }
  });
});

describe("other methods", () => {
  for (const method of ["POST", "PUT", "DELETE"])
    for (const path of ["/", "/latest"])
      it(`answers 405 to ${method} ${path}`, async () => {
        const res = await run(`https://padmate.download${path}`, { method });
        assert.equal(res.status, 405);
        assert.equal(res.headers.get("allow"), "GET, HEAD");
        assert.equal(fetchCalls.length, 0);
      });
});

// ---- Other paths ----------------------------------------------------------

describe("paths that are not the Worker's", () => {
  for (const [path, method] of [
    ["/latest.yml", "GET"],
    [`/${name}`, "GET"],
    ["/x.sha256", "GET"],
    ["/latest/", "GET"],
    ["/LATEST", "GET"],
    ["/index.html", "GET"],
    ["/latest.yml", "POST"],
  ])
    it(`passes ${method} ${path} on to fetch untouched`, async () => {
      const sentinel = new Response("from R2");
      world.other = () => sentinel;
      world.latest = () => sentinel;
      const request = new Request(`https://padmate.download${path}`, { method });
      const res = await handle(request, {}, makeCtx());
      assert.equal(res, sentinel);
      assert.equal(fetchCalls.length, 1);
      assert.equal(fetchCalls[0].input, request);
      assert.equal(fetchCalls[0].init, undefined);
    });
});

// ---- The Cache API --------------------------------------------------------

describe("failures of the Cache API", () => {
  it("are a miss: match and put reject", async () => {
    globalThis.caches = {
      default: {
        match: async () => {
          throw new Error("cache down");
        },
        put: async () => {
          throw new Error("cache down");
        },
      },
    };
    const res = await page("/?lang=zh");
    assert.equal(res.status, 200);
    const html = await res.text();
    assert.ok(html.includes(`href="/${name}"`));
    assert.ok(githubCard(html).includes('<span class="mono">91</span>'));
    assert.equal((await run("https://padmate.download/latest")).status, 302);
  });

  it("are a miss: match throws at once", async () => {
    globalThis.caches = {
      default: {
        match() {
          throw new Error("cache down");
        },
        put: async () => {},
      },
    };
    assert.equal((await page("/")).status, 200);
  });

  it("are a miss: there is no Cache API at all", async () => {
    delete globalThis.caches;
    const res = await page("/?lang=en");
    assert.equal(res.status, 200);
    assert.ok((await res.text()).includes(`href="/${name}"`));
    assert.equal((await run("https://padmate.download/latest")).status, 302);
  });

  it("are a miss: caches has no default", async () => {
    globalThis.caches = {};
    assert.equal((await page("/")).status, 200);
  });

  it("do not need a ctx: the write is then awaited", async () => {
    const res = await handle(
      new Request("https://padmate.download/latest"),
      {},
      undefined,
    );
    assert.equal(res.status, 302);
    assert.ok(cacheStore.size > 0);
  });
});
