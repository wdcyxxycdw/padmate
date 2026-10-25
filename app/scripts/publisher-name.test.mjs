import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// daemon/internal/authenticode/testdata/updater-publisher.json says which
// publisherName electron-updater accepts for which signer subject. The Go side
// (authenticode.UpdaterAccepts, which `packageinstaller verify` uses on
// resources/app-update.yml) is a port of this comparison; this test computes
// the same answers with the real parseDn of builder-util-runtime, the library
// electron-updater uses, so the table, the port and the library cannot drift
// apart. The comparison itself is copied from
// electron-updater/out/windowsExecutableCodeSignatureVerifier.js
// (verifySignature, electron-updater 6.8.9).
const here = dirname(fileURLToPath(import.meta.url));
const require = createRequire(import.meta.url);
const { parseDn } = require(
  resolve(here, "../node_modules/builder-util-runtime"),
);
const table = JSON.parse(
  readFileSync(
    resolve(
      here,
      "../../daemon/internal/authenticode/testdata/updater-publisher.json",
    ),
    "utf8",
  ),
);

function updaterAccepts(signerSubject, publisherName) {
  const subject = parseDn(signerSubject);
  const dn = parseDn(publisherName);
  if (dn.size) {
    return Array.from(dn.keys()).every(
      (key) => dn.get(key) === subject.get(key),
    );
  }
  return publisherName === subject.get("CN");
}

describe("the publisherName table of the Go side", () => {
  it("has accepted and rejected cases", () => {
    expect(table.length).toBeGreaterThanOrEqual(50);
    expect(table.some((c) => c.accepts)).toBe(true);
    expect(table.some((c) => !c.accepts)).toBe(true);
  });

  it.each(table.map((c) => [c.subject, c.publisherName, c.accepts]))(
    "signer %j, publisherName %j: accepted=%s",
    (subject, publisherName, accepts) => {
      expect(updaterAccepts(subject, publisherName)).toBe(accepts);
    },
  );

  it("knows the quirks the Go port has to keep", () => {
    const fixture = "CN=padmate fixture, O=padmate contributors, C=JP";
    // Exact: case, spaces and the key spelling count.
    expect(updaterAccepts(fixture, "PADMATE FIXTURE")).toBe(false);
    expect(updaterAccepts(fixture, " padmate fixture ")).toBe(false);
    expect(updaterAccepts(fixture, "cn=padmate fixture")).toBe(false);
    // Only the attributes that are listed are compared.
    expect(updaterAccepts(fixture, "CN=padmate fixture")).toBe(true);
    // Windows writes the state as S=.
    const tokyo = "CN=Example, O=Example, S=Tokyo, C=JP";
    expect(updaterAccepts(tokyo, "S=Tokyo")).toBe(true);
    expect(updaterAccepts(tokyo, "ST=Tokyo")).toBe(false);
  });
});
