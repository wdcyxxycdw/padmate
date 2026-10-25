import { createRequire } from "node:module";
import { createHttpError, HttpError } from "builder-util-runtime";
import { GitHubProvider } from "electron-updater/out/providers/GitHubProvider";
import { describe, expect, it } from "vitest";
import { isNoReleaseError } from "./update-config";

// What electron-updater 6.8.9 does with the releases of a repository, which
// UpdateController (update-controller.ts) is written around: the GitHub
// provider is driven by the real code with canned answers in place of the
// network. A new version of the package that behaves otherwise fails here,
// before it reaches the application (ADR-0073, PKG-10).
const require = createRequire(import.meta.url);
const updaterRequire = createRequire(require.resolve("electron-updater"));
const semver = updaterRequire("semver") as { parse(version: string): unknown };

interface Release {
  tag: string;
  /** Channel files this release has: name -> version it says. */
  files: Record<string, string>;
}

/** The error electron-updater's executor raises for an answer with this status. */
function httpError(statusCode: number, statusMessage: string) {
  return createHttpError(
    {
      statusCode,
      statusMessage,
      headers: { "content-length": "404" },
    } as never,
    statusCode === 404
      ? "method: GET url: https://github.com/o/r/releases/latest"
      : null,
  );
}

/** The releases of the repository, newest first, as GitHub lists them. */
function repository(
  releases: Release[],
  latest: string | null | Error,
  atom?: Error,
) {
  const requested: string[] = [];
  const executor = {
    request: async (options: { path?: string }) => {
      const path = options.path ?? "";
      requested.push(path);
      if (path === "/o/r/releases.atom") {
        if (atom) throw atom;
        return `<feed>${releases
          .map(
            (r) =>
              `<entry><title>${r.tag}</title><link rel="alternate" href="https://github.com/o/r/releases/tag/${r.tag}"/><content>notes</content></entry>`,
          )
          .join("")}</feed>`;
      }
      if (path === "/o/r/releases/latest") {
        // GitHub answers 404 when every release is a prerelease.
        if (latest === null) throw httpError(404, "Not Found");
        if (latest instanceof Error) throw latest;
        return JSON.stringify({ tag_name: latest });
      }
      for (const release of releases)
        for (const [file, version] of Object.entries(release.files))
          if (path === `/o/r/releases/download/${release.tag}/${file}`)
            return `version: ${version}\nfiles:\n  - url: padmate-v${version}-windows-amd64-setup.exe\n    sha512: AAAA\n    size: 1\npath: padmate-v${version}-windows-amd64-setup.exe\nsha512: AAAA\nreleaseDate: '2026-10-24T00:00:00.000Z'\n`;
      throw new HttpError(404);
    },
  };
  return { executor, requested };
}

async function look(
  releases: Release[],
  options: {
    current: string;
    allowPrerelease: boolean;
    latest: string | null | Error;
    atom?: Error;
  },
) {
  const { executor, requested } = repository(
    releases,
    options.latest,
    options.atom,
  );
  const updater = {
    allowPrerelease: options.allowPrerelease,
    channel: null,
    currentVersion: semver.parse(options.current),
    fullChangelog: false,
  };
  const provider = new GitHubProvider(
    { provider: "github", owner: "o", repo: "r" },
    updater as never,
    { executor, platform: "win32", isUseMultipleRangeRequest: false } as never,
  );
  const info = await provider.getLatestVersion();
  return { version: info.version, tag: info.tag, requested };
}

// electron-builder writes only latest.yml for a GitHub release, whatever the
// version: no rc.yml (build-installer.md).
const stable = (version: string): Release => ({
  tag: `v${version}`,
  files: { "latest.yml": version },
});

describe("electron-updater's GitHub provider, as the controller relies on it (PKG-10)", () => {
  it("never offers a release to an application that is a release candidate when it follows the prereleases", async () => {
    const releases = [
      stable("1.0.0"),
      stable("1.0.0-rc.2"),
      stable("1.0.0-rc.1"),
    ];
    // The first look of a release candidate: among its own channel.
    const among = await look(releases, {
      current: "1.0.0-rc.1",
      allowPrerelease: true,
      latest: "v1.0.0",
    });
    expect(among.version).toBe("1.0.0-rc.2");
    // At the newest candidate there is nothing newer in that channel: it
    // sees itself, which is no update.
    const newest = await look(releases, {
      current: "1.0.0-rc.2",
      allowPrerelease: true,
      latest: "v1.0.0",
    });
    expect(newest.version).toBe("1.0.0-rc.2");
  });

  it("offers the release on the second look, among releases only", async () => {
    const releases = [
      stable("1.0.0"),
      stable("1.0.0-rc.2"),
      stable("1.0.0-rc.1"),
    ];
    const second = await look(releases, {
      current: "1.0.0-rc.2",
      allowPrerelease: false,
      latest: "v1.0.0",
    });
    expect(second.version).toBe("1.0.0");
    // It asks which release is the latest one, and reads latest.yml of that.
    expect(second.requested).toContain("/o/r/releases/latest");
    expect(second.requested.at(-1)).toBe(
      "/o/r/releases/download/v1.0.0/latest.yml",
    );
  });

  it("falls back to latest.yml when a candidate's channel file is not there", async () => {
    const releases = [stable("1.0.0-rc.2"), stable("1.0.0-rc.1")];
    const result = await look(releases, {
      current: "1.0.0-rc.1",
      allowPrerelease: true,
      latest: "v1.0.0-rc.2",
    });
    expect(result.version).toBe("1.0.0-rc.2");
    expect(result.requested.at(-2)).toBe(
      "/o/r/releases/download/v1.0.0-rc.2/rc.yml",
    );
    expect(result.requested.at(-1)).toBe(
      "/o/r/releases/download/v1.0.0-rc.2/latest.yml",
    );
  });

  it("offers a release to an application that is a release, without looking at prereleases", async () => {
    const releases = [stable("1.0.1-rc.1"), stable("1.0.0"), stable("0.9.0")];
    const result = await look(releases, {
      current: "0.9.0",
      allowPrerelease: false,
      latest: "v1.0.0",
    });
    expect(result.version).toBe("1.0.0");
    expect(result.tag).toBe("v1.0.0");
  });

  it("answers the first look and fails the second while only prereleases are published", async () => {
    // The controller takes this failure of the second look for "nothing
    // newer" (isNoReleaseError), and no other.
    const releases = [stable("1.0.0-rc.2"), stable("1.0.0-rc.1")];
    const first = await look(releases, {
      current: "1.0.0-rc.2",
      allowPrerelease: true,
      latest: null,
    });
    expect(first.version).toBe("1.0.0-rc.2");
    const second = await look(releases, {
      current: "1.0.0-rc.2",
      allowPrerelease: false,
      latest: null,
    }).catch((error: unknown) => error);
    expect(second).toBeInstanceOf(Error);
    expect(isNoReleaseError(second)).toBe(true);
  });

  it("does not take any other failure of the second look for a missing release", async () => {
    const releases = [stable("1.0.0-rc.2"), stable("1.0.0-rc.1")];
    const base = { current: "1.0.0-rc.2", allowPrerelease: false } as const;
    const failures = {
      "a server error on the latest release": look(releases, {
        ...base,
        latest: httpError(503, "Service Unavailable"),
      }),
      "a rate limit": look(releases, {
        ...base,
        latest: httpError(429, "Too Many Requests"),
      }),
      "a connection that dropped": look(releases, {
        ...base,
        latest: Object.assign(new Error("read ECONNRESET"), {
          code: "ECONNRESET",
        }),
      }),
      // A private repository: the list of releases itself is not there.
      "a repository that cannot be read": look(releases, {
        ...base,
        latest: null,
        atom: httpError(404, "Not Found"),
      }),
    };
    for (const [reason, pending] of Object.entries(failures)) {
      const error = await pending.catch((e: unknown) => e);
      expect(error, reason).toBeInstanceOf(Error);
      expect(isNoReleaseError(error), reason).toBe(false);
    }
  });

  it("fails when the repository has no release it can read (a private repository answers 404)", async () => {
    const failing = {
      request: async () => {
        throw new HttpError(404);
      },
    };
    const provider = new GitHubProvider(
      { provider: "github", owner: "o", repo: "r" },
      {
        allowPrerelease: false,
        channel: null,
        currentVersion: semver.parse("1.0.0"),
        fullChangelog: false,
      } as never,
      {
        executor: failing,
        platform: "win32",
        isUseMultipleRangeRequest: false,
      } as never,
    );
    await expect(provider.getLatestVersion()).rejects.toThrow();
  });
});
