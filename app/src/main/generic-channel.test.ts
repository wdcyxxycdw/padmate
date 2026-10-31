import { createRequire } from "node:module";
import { createHttpError, type HttpError } from "builder-util-runtime";
import { GenericProvider } from "electron-updater/out/providers/GenericProvider";
import { describe, expect, it } from "vitest";
import { UPDATE_CHANNEL, UPDATE_URL } from "./update-config";

// What electron-updater 6.8.9 does with the download site, which
// UpdateController (update-controller.ts) is written around: the generic
// provider is driven by the real code with canned answers in place of the
// network. A new version of the package that behaves otherwise fails here,
// before it reaches the application (ADR-0078, PKG-10).
const require = createRequire(import.meta.url);
const updaterRequire = createRequire(require.resolve("electron-updater"));
const semver = updaterRequire("semver") as { parse(version: string): unknown };

/** The latest.yml electron-builder writes next to the installer. */
function latestYml(version: string): string {
  const installer = `padmate-v${version}-windows-amd64-setup.exe`;
  return `version: ${version}\nfiles:\n  - url: ${installer}\n    sha512: AAAA\n    size: 1\npath: ${installer}\nsha512: AAAA\nreleaseDate: '2026-10-31T00:00:00.000Z'\n`;
}

interface RequestOptions {
  protocol?: string;
  hostname?: string;
  port?: string;
  path?: string;
}

/**
 * The download site: the files in the bucket by path, and the answer to
 * everything else a 404. `failure` is what every request fails with instead.
 */
function site(files: Record<string, string>, failure?: Error) {
  const requested: URL[] = [];
  const executor = {
    request: async (options: RequestOptions) => {
      const port = options.port ? `:${options.port}` : "";
      const url = new URL(
        `${options.protocol}//${options.hostname}${port}${options.path}`,
      );
      requested.push(url);
      if (failure) throw failure;
      const body = files[url.pathname];
      if (body === undefined)
        throw createHttpError({
          statusCode: 404,
          statusMessage: "Not Found",
          headers: {},
        } as never);
      return body;
    },
  };
  return { executor, requested };
}

/**
 * The provider for an application of this version, made the way
 * updater-electron.ts configures it: the constants of update-config.ts, and an
 * updater that has not set a channel of its own.
 */
function providerFor(
  current: string,
  bucket: ReturnType<typeof site>,
  configuration: { url: string; channel?: string } = {
    url: UPDATE_URL,
    channel: UPDATE_CHANNEL,
  },
) {
  const updater = {
    // electron-updater sets this for a version with a prerelease part, as
    // 1.0.3-rc.1 has; the generic provider never reads it.
    allowPrerelease: current.includes("-"),
    channel: null,
    currentVersion: semver.parse(current),
    isAddNoCacheQuery: true,
  };
  return new GenericProvider(
    { provider: "generic", ...configuration },
    updater as never,
    {
      executor: bucket.executor,
      platform: "win32",
      isUseMultipleRangeRequest: false,
    } as never,
  );
}

describe("electron-updater's generic provider, as the controller relies on it (PKG-10, ADR-0078)", () => {
  it.each(["1.0.2", "1.0.3-rc.1"])(
    "asks for latest.yml once, whichever version %s is running",
    async (current) => {
      const bucket = site({ "/latest.yml": latestYml("1.0.3") });
      const info = await providerFor(current, bucket).getLatestVersion();
      expect(info.version).toBe("1.0.3");
      expect(bucket.requested).toHaveLength(1);
      const [request] = bucket.requested;
      expect(request.protocol).toBe("https:");
      expect(request.host).toBe("padmate.download");
      expect(request.pathname).toBe("/latest.yml");
    },
  );

  it("reads latest.yml also when the address has no slash at the end", async () => {
    // electron-updater adds the slash; the constant has it already, because
    // the installer's relative path is resolved against it.
    const bucket = site({ "/latest.yml": latestYml("1.0.3") });
    await providerFor("1.0.2", bucket, {
      url: "https://padmate.download",
      channel: "latest",
    }).getLatestVersion();
    expect(bucket.requested.map((url) => url.pathname)).toEqual([
      "/latest.yml",
    ]);
  });

  it("is why the channel is written down: another channel is another file", async () => {
    // electron-builder derives the channel from the prerelease part of the
    // version when it is not given (1.0.3-rc.1 would bake in rc), and the
    // site holds latest.yml only.
    const bucket = site({ "/latest.yml": latestYml("1.0.3") });
    const error = await providerFor("1.0.3-rc.1", bucket, {
      url: UPDATE_URL,
      channel: "rc",
    })
      .getLatestVersion()
      .catch((e: unknown) => e);
    expect(bucket.requested.map((url) => url.pathname)).toEqual(["/rc.yml"]);
    expect(error).toMatchObject({ code: "ERR_UPDATER_CHANNEL_FILE_NOT_FOUND" });
    // No channel at all is latest.yml, but the constant does not rely on it.
    const plain = site({ "/latest.yml": latestYml("1.0.3") });
    await providerFor("1.0.3-rc.1", plain, {
      url: UPDATE_URL,
    }).getLatestVersion();
    expect(plain.requested.map((url) => url.pathname)).toEqual(["/latest.yml"]);
  });

  it("puts a time in the query of latest.yml, and none in the installer's address", async () => {
    // So the site must not be asked to cache by path alone: Cloudflare keeps
    // the query in the cache key, and latest.yml is uploaded no-cache.
    const before = Date.now();
    const bucket = site({ "/latest.yml": latestYml("1.0.3") });
    const provider = providerFor("1.0.2", bucket);
    const info = await provider.getLatestVersion();
    const after = Date.now();
    const [request] = bucket.requested;
    expect(request.search).toMatch(/^\?noCache=[0-9a-v]+$/);
    const time = Number.parseInt(request.searchParams.get("noCache") ?? "", 32);
    expect(time).toBeGreaterThanOrEqual(before);
    expect(time).toBeLessThanOrEqual(after);
    const [file] = provider.resolveFiles(info);
    expect(file.url.search).toBe("");
  });

  it("resolves the installer of latest.yml against the address of the site", async () => {
    const bucket = site({ "/latest.yml": latestYml("1.0.3") });
    const provider = providerFor("1.0.2", bucket);
    const files = provider.resolveFiles(await provider.getLatestVersion());
    expect(files.map((file) => file.url.href)).toEqual([
      "https://padmate.download/padmate-v1.0.3-windows-amd64-setup.exe",
    ]);
    expect(files[0].info.sha512).toBe("AAAA");
  });

  it("fails with ERR_UPDATER_CHANNEL_FILE_NOT_FOUND when the site has no latest.yml", async () => {
    // A bucket without latest.yml is a failed check, not "nothing newer".
    const bucket = site({});
    const error = await providerFor("1.0.2", bucket)
      .getLatestVersion()
      .catch((e: unknown) => e);
    expect(error).toBeInstanceOf(Error);
    expect(error).toMatchObject({ code: "ERR_UPDATER_CHANNEL_FILE_NOT_FOUND" });
    expect((error as Error).message).not.toContain(
      "please ensure a production release exists",
    );
  });

  it.each([
    ["a server error", createError(503, "Service Unavailable")],
    ["a rate limit", createError(429, "Too Many Requests")],
    ["a refusal by the zone's rules", createError(403, "Forbidden")],
    [
      "a connection that dropped",
      Object.assign(new Error("read ECONNRESET"), { code: "ECONNRESET" }),
    ],
  ])(
    "passes %s on as it is, not as a missing file",
    async (_reason, failure) => {
      const bucket = site({}, failure);
      const error = await providerFor("1.0.2", bucket)
        .getLatestVersion()
        .catch((e: unknown) => e);
      expect(error).toBe(failure);
      expect(error).not.toMatchObject({
        code: "ERR_UPDATER_CHANNEL_FILE_NOT_FOUND",
      });
      expect(bucket.requested).toHaveLength(1);
    },
  );

  it("fails on a latest.yml that is not YAML", async () => {
    const bucket = site({ "/latest.yml": "version: [1.0.3\nfiles: {" });
    await expect(
      providerFor("1.0.2", bucket).getLatestVersion(),
    ).rejects.toMatchObject({ code: "ERR_UPDATER_INVALID_UPDATE_INFO" });
  });
});

/** The error electron-updater's executor raises for an answer with this status. */
function createError(statusCode: number, statusMessage: string): HttpError {
  return createHttpError({
    statusCode,
    statusMessage,
    headers: { "content-length": "0" },
  } as never);
}
