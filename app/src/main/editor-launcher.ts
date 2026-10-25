import { spawn } from "node:child_process";
import { appendFile, lstat } from "node:fs/promises";
import { extname, isAbsolute, join, resolve } from "node:path";

export interface EditorLauncherOptions {
  // The program Windows runs for vscode:// links, from
  // app.getApplicationInfoForProtocol: VS Code's own executable when it is
  // installed. Rejects or resolves to "" when nothing handles the scheme.
  vscode: () => Promise<string>;
  // %SystemRoot%, where Notepad is.
  systemRoot: string | undefined;
  // Starts file with args, detached and without a shell; resolves once the
  // process has started.
  start: (file: string, args: string[]) => Promise<void>;
  isFile?: (path: string) => Promise<boolean>;
}

async function regularFile(path: string): Promise<boolean> {
  try {
    return (await lstat(path)).isFile();
  } catch {
    return false;
  }
}

/**
 * Opens a file in an editor (SCR-50, design §10): VS Code when Windows
 * names it as the handler of vscode:// (as Explorer's "Open with Code"
 * does: its executable with the file as the argument), otherwise Notepad.
 * Neither goes through a shell or the file's association, which often
 * opens .ts files in a video player, nor a vscode://file link, which VS
 * Code asks about every time. Resolves to whether an editor started.
 */
export function editorLauncher(
  options: EditorLauncherOptions,
): (path: string) => Promise<boolean> {
  const isFile = options.isFile ?? regularFile;
  const attempt = async (program: string, path: string) => {
    try {
      await options.start(program, [path]);
      return true;
    } catch {
      return false;
    }
  };
  return async (path) => {
    let code = "";
    try {
      code = await options.vscode();
    } catch {
      code = "";
    }
    if (
      typeof code === "string" &&
      isAbsolute(code) &&
      resolve(code) === code &&
      extname(code).toLowerCase() === ".exe" &&
      (await isFile(code)) &&
      (await attempt(code, path))
    )
      return true;
    const root = options.systemRoot;
    if (!root || !isAbsolute(root)) return false;
    const notepad = join(resolve(root), "System32", "notepad.exe");
    return (await isFile(notepad)) && attempt(notepad, path);
  };
}

/**
 * Starts an editor detached from padmate, without a shell. Electron's own
 * variables are left out: with ELECTRON_RUN_AS_NODE, VS Code (an Electron
 * app) would run the file as a Node.js program.
 */
export function startDetached(file: string, args: string[]): Promise<void> {
  const env: NodeJS.ProcessEnv = {};
  for (const [key, value] of Object.entries(process.env))
    if (!/^ELECTRON_/i.test(key)) env[key] = value;
  return new Promise((done, fail) => {
    const child = spawn(file, args, {
      detached: true,
      shell: false,
      stdio: "ignore",
      env,
    });
    child.once("error", fail);
    child.once("spawn", () => {
      child.unref();
      done();
    });
  });
}

/**
 * A stand-in for the editor in development and E2E runs
 * (PADMATE_UI_EDITOR_LOG): it appends each path to the log file, one per
 * line, and starts nothing.
 */
export function editorRecorder(
  log: string,
): (path: string) => Promise<boolean> {
  return async (path) => {
    await appendFile(log, `${path}\n`, "utf8");
    return true;
  };
}
