# ADR-0011: Scripts as macros

[中文](0011-script-macros.zh-CN.md)

Related: SCR-50, SCR-51; [ADR: Scripting runtime](0010-scripting-runtime.md)

## Context

Early scripts were one entry per profile that took over the buttons as a whole, and declarative bindings could only call the named actions it registered. What users want is "press this button, do one thing". Besides, the UI had to write a file other than profile.json into the library for the first time, and to read files the user picks in a native dialog. The renderer has no Node capabilities, and file writes need a single writer.

## Decision

1. **A file is a macro.** Each `.ts`/`.js` at the top of the script library `<data folder>/macros/` is a macro. Its default export is a function without parameters, run once from the start when its bound button fires. The binding is the action `{"type":"macro","file":…}`, with no parameter or version fields. A macro can use only the members marked `@macro` in the d.ts, and cannot register handlers or outlive one run.
2. **One run, one owner.** While a macro has not finished on a controller, firing it again does nothing. A run ends when the function, or the Promise it returns, completes: keys still held are released, and taps already handed to the Arbiter finish as usual. A run lasts at most 60 seconds.
3. **Pause, switching profiles, reload, disconnect, emergency release and exit all release the whole runtime, including a macro that is running (invariant 1).**
4. **Each macro file is built on its own and runs in the same runtime.** An error in one file does not affect the others. Each controller still has one goja runtime, sharing the lifecycle, the watchdog and the output rate limit. With macros only, the runtime does not take over input, and the latency of ordinary bindings is unchanged.
5. **The script library does not belong to a profile.** All profiles share one library and only refer to it. Renaming and deleting change the references in every profile at once, under the library's write lock; if any step fails, everything is changed back. Writes to the library go through `os.Root`, and the name must appear exactly in the directory listing first, so case, 8.3 short names, links and junctions never reach another file.
6. **Exporting includes the scripts. On import, a script with the same name and content is reused; for the same name with other content, the UI asks "Replace" or "Keep both".**
7. **When the UI creates or imports a script, only the daemon writes the file.** Electron's Main picks and reads the file (at most 1 MiB, strict UTF-8); the renderer gets a relative file name only, never a path. Replacing a script of the same name uses a one-time token: Main keeps what it read in memory and, on confirmation, writes exactly that, without reading the file again or accepting a path from the renderer.

## Costs

- Pressing again before a macro finishes is ignored; a macro cannot do something that goes on for as long as the button is held.
- When the watchdog interrupts a macro, the whole runtime restarts with back-off, and every macro and legacy script on that controller is interrupted; the interrupted macro stays disabled until its file changes.
- When the foreground program switches profiles, a running macro is interrupted, including when the macro's own Alt+Tab caused the switch.
- Renaming or deleting writes profile.json and the files in two steps; a failure in between can leave a reference to a missing file, which the UI shows as "not found". Undo, or restoring a history version, can also bring back such a reference.
- Imported scripts get no trust confirmation; the risk is the same as importing someone else's scripts.
- Export finds modules by the relative imports in the text, and cannot find computed paths.

## Alternatives given up

- **Several entry scripts running at once.** Who wins when they want the same key would need rules of its own, and it is not how users want to work.
- **All macros bundled into one entry.** One syntax error would disable every macro, and output could not be told apart by macro.
- **A runtime per macro.** Several runtimes, cold starts and watchdogs per controller, doubling memory and code.
- **Queuing or restarting a macro that is fired while it runs.** A queue runs a long macro again seconds later; restarting leaves effects half done.
- **Canceling everything of the owner when a run ends.** A last `key.tap` that was not awaited would be cut short, and keys of other owners would be released early.
- **Scripts stored per profile.** The same script would need several copies, and the page could not show the scripts of other profiles.
- **On import, always replace or always keep the same name.** Replacing changes how other profiles behave; keeping fills the library with copies.
- **The renderer reads the files itself, or confirming a replacement reads the path again.** Main's checks could not be reused, and what was confirmed and what is written might not be the same file.
- **"Edit" with the default program for `.ts`.** On Windows that is often a video player.
