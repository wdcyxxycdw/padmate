// A guard of the main process bundle (PKG-10, ADR-0073).
//
// The installed application has no node_modules (electron-builder.yml packs
// none), so every package the main process uses must be bundled into
// out/main/index.js. electron-vite leaves the dependencies of package.json
// out of the bundle unless electron.vite.config.ts excludes them from
// externalizeDepsPlugin, and an application that requires a package it does
// not have fails at its first start. This reads the bundle and names any
// require that is not Node's or electron's. The same rule is applied to the
// installed app.asar by daemon/internal/installerpkg (MainBundleRequires).

import { readFile } from "node:fs/promises";
import { builtinModules } from "node:module";

const allowed = new Set([
  ...builtinModules,
  ...builtinModules.map((name) => `node:${name}`),
  "electron",
]);

/**
 * The modules that code requires and an installed application cannot find,
 * each once, in order of appearance. "<dynamic>" stands for a require whose
 * argument is not a plain string.
 */
export function bareRequires(code) {
  const found = [];
  const add = (name) => {
    if (!found.includes(name)) found.push(name);
  };
  for (const match of code.matchAll(/(?<![\w$.])require\(\s*([^)]*?)\s*\)/g)) {
    const literal = /^(["'`])([^"'`]*)\1$/.exec(match[1]);
    if (!literal) add("<dynamic>");
    else if (!allowed.has(literal[2])) add(literal[2]);
  }
  return found;
}

/** Throws, naming them, when the bundle at file requires what is not there. */
export async function assertMainBundle(file) {
  const bare = bareRequires(await readFile(file, "utf8"));
  if (bare.length > 0)
    throw new Error(
      `${file} requires ${bare.map((name) => JSON.stringify(name)).join(", ")}, which the installed application does not have (it has no node_modules). Bundle it: add it to the exclude list of externalizeDepsPlugin in electron.vite.config.ts`,
    );
}
