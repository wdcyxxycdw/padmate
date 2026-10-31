import { afterEach, describe, expect, it, vi } from "vitest";
import worker, { INSTALLER, installerFromLatest, respond } from "./index.js";

const name = "padmate-v1.0.3-windows-amd64-setup.exe";
const latest = `version: 1.0.3\nfiles:\n  - url: ${name}\n    sha512: AAAA\npath: ${name}\nsha512: AAAA\nreleaseDate: '2026-10-31T01:02:03.456Z'\n`;
const unavailable =
  "padmate: the download is not available right now. Please try again later.";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("installerFromLatest", () => {
  it("reads the installer from the path line of latest.yml", () => {
    expect(installerFromLatest(latest)).toBe(name);
    expect(installerFromLatest(latest.replaceAll("\n", "\r\n"))).toBe(name);
    expect(installerFromLatest(`path: ${name}   \n`)).toBe(name);
  });

  it("reads a prerelease name too: the redirect follows what latest.yml says", () => {
    expect(
      installerFromLatest(
        "path: padmate-v1.0.3-rc.1-windows-amd64-setup.exe\n",
      ),
    ).toBe("padmate-v1.0.3-rc.1-windows-amd64-setup.exe");
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
      expect(installerFromLatest(text), JSON.stringify(text)).toBeNull();
  });

  it("is the pattern of the deployed Worker", () => {
    expect(INSTALLER.source).toBe(
      String.raw`^padmate-v\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?-windows-amd64-setup\.exe$`,
    );
  });
});

describe("respond", () => {
  const request = new Request("https://padmate.download/");
  const answer = (body, init) => async () => new Response(body, init);

  it("redirects with a 302 to the installer latest.yml names, never cached", async () => {
    const res = await respond(request, answer(latest));
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBe(
      `https://padmate.download/${name}`,
    );
    expect(res.headers.get("cache-control")).toBe("no-cache");
  });

  it("redirects a visitor who came over http to the installer over https", async () => {
    // Chrome warns about, and may block, a download that is not over https.
    const calls = [];
    const res = await respond(
      new Request("http://padmate.download/"),
      async (url) => {
        calls.push(String(url));
        return new Response(latest);
      },
    );
    expect(res.headers.get("location")).toBe(
      `https://padmate.download/${name}`,
    );
    expect(calls).toEqual(["https://padmate.download/latest.yml"]);
  });

  it("asks for /latest.yml on the same host, from the edge cache for a minute", async () => {
    const calls = [];
    await respond(request, async (url, init) => {
      calls.push([String(url), init]);
      return new Response(latest);
    });
    expect(calls).toEqual([
      ["https://padmate.download/latest.yml", { cf: { cacheTtl: 60 } }],
    ]);
  });

  it("answers 503 when latest.yml cannot be had or does not name an installer", async () => {
    const failures = [
      answer("not found", { status: 404 }),
      answer("server error", { status: 500 }),
      answer("<html>a challenge</html>", { status: 200 }),
      answer("path: evil.exe\n"),
      async () => {
        throw new Error("connection reset");
      },
    ];
    for (const fetchLatest of failures) {
      const res = await respond(request, fetchLatest);
      expect(res.status).toBe(503);
      expect(res.headers.get("cache-control")).toBe("no-store");
      expect(res.headers.get("content-type")).toMatch(/^text\/plain/);
      expect(await res.text()).toBe(unavailable);
      // It is not a redirect, whatever else went wrong.
      expect(res.headers.get("location")).toBeNull();
    }
  });
});

describe("the Worker", () => {
  it("answers a request with the global fetch", async () => {
    const fetch = vi.fn(async () => new Response(latest));
    vi.stubGlobal("fetch", fetch);
    const res = await worker.fetch(new Request("https://padmate.download/"));
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBe(
      `https://padmate.download/${name}`,
    );
    expect(String(fetch.mock.calls[0][0])).toBe(
      "https://padmate.download/latest.yml",
    );
  });
});
