# ADR-0014: Overlay window

[中文](0014-overlay.zh-CN.md)

Related: [ARCHITECTURE §12](../ARCHITECTURE.md); [ADR: Scripting runtime](0010-scripting-runtime.md); [ADR: Processes and stack](0001-processes-and-stack.md)

## Context

The overlay shows, above the painting app, toasts, the list of shortcuts while the "Key hints" button is held, and scripts' toasts and panels.

It can go wrong in only one way, and that is the worst one: taking the foreground. Foreground detection treats "the foreground window belongs to a registered UI process" as padmate itself being in the foreground, and then the Arbiter only keeps virtual accounts and sends no keys. The overlay and the main window both belong to padmate.exe, so if the overlay is activated even once, the controller stops sending keys to the painting app until the user clicks back. While painting, users do not click windows; all they see is "the buttons suddenly stopped working".

## Decision

### 1. The window is never activated
- The overlay is padmate.exe's second `BrowserWindow`, created once and living as long as the app. It is part of the optional UI: no UI, no overlay.
- The window cannot be focused (`WS_EX_NOACTIVATE`), stays on top, and lets the mouse through without forwarding. It does not forward because `forward:true` installs a `WH_MOUSE_LL` hook in the UI process, and when that thread stalls, the mouse slows down for the whole system.
- **It is shown once in its whole life, with `showInactive()`.** The code has no `show()`, `focus()`, `moveTop()` or `hide()` (a test reads the source to check). The page shows and hides its content by switching `data-visible`, so no path can activate it.
- It does not cover the screen, since Windows treats a full-screen topmost window as a full-screen program. The window is centered at the bottom or top of the primary display's work area, its height follows its content, and only `setBounds` moves it.
- When the renderer process crashes, it is rebuilt once; after a second crash it is not, since a crash loop is worse than no hints.

### 2. The channel goes only from Main to the page
The overlay's preload exposes receiving functions only, with no `send` or `invoke`, so the page cannot call Main. The preload checks every message field by field, and the page renders text as text nodes. Code in Main that hangs on daemon events must swallow its own exceptions, or it would break the connection to the daemon.

### 3. Toasts and preferences
- Toasts come from the `state` event of `engine.activity`: the profile name changes, pause or resume, the base layer changes. Layers pushed by hold or toggle get no toast, or holding a layer while painting would make it flash all the time. The first state after connecting is the starting point and shows no toast.
- Position, opacity and the switch for notices are the UI's own preferences, stored in the renderer and pushed by the UI to Main; they go neither into config.json nor into a profile. Opacity fades only the background and does not use `BrowserWindow.setOpacity()`.

### 4. Key hints
- The action `{"type":"hud","op":"hints"}` sends no key and can only be on `press` and `hold` gestures. Its state works the same way as precision mode, and `CancelAll` clears it too, so release, pause, reload and disconnect all end it.
- The daemon computes the list, since it is the only authority on the resolved layer stack. It recomputes only when hints turn on or off and when the layer stack changes, never on the hot path. The slot object of the `state` event has an optional `hints`, present only while hints are on, at most 250 ms behind.
- The renderer translates control names for the controller, with the same name table as the Mapping page.

### 5. Scripts' toasts and panels
- `host.has("hud")` is true exactly when a client subscribes to the topic `hud`, and Main subscribes only after the overlay page has been drawn, so when it is true an overlay is really on the screen.
- Script toasts follow the "Notices" switch, whether or not the main window has focus. Panels are published with the `state` event and follow the same slot as key hints; panels step aside while key hints are shown.

## Costs

- The overlay cannot be seen over exclusive full-screen programs (a known limitation).
- On Windows virtual desktops, the overlay shows only on the desktop that created it.
- Hints and panels can be up to 250 ms late (the pace of the state event).
- It follows only the first mapped slot, and with several controllers it does not say which one.
- Text the daemon adds (`层：`, `配置：`) does not follow the UI's language.
- When a padmate window enters or leaves the foreground, the slot does `ReleaseAll`; the script runtime is invalidated and its panels disappear until the next press rebuilds them.
- How `setBounds` behaves on displays with mixed DPI has not been tested.

## Alternatives given up

- **A full-screen transparent window.** It would be treated as a full-screen program.
- **`show()` on every display and `hide()` on every hide.** The most obvious, and the easiest way to steal focus.
- **The daemon draws the overlay itself (a layered Win32 window).** Text and layout would have to be done in Go.
- **Mouse pass-through with `forward: true`.** It needs a global mouse hook, while the overlay has nothing to hover.
- **Preferences in config.json, or Main reading `localStorage`.** The first changes the schema and the meaning of hot reload; the second needs polling or one more round trip per toast.
- **`setOpacity()`, or opacity on the root element.** It stacks with the page's fades and breaks the readings of E2E.
- **A second overlay window for panels.** It breaks "created once".
- **The UI computes the list from the profile and layer state, or the list is always in the state event.** The first is a second authority; the second sends a few KB more every 250 ms that nobody wants nearly all the time.
- **Panels with "add" and "remove" events, or a new `hud.attach`.** See [Scripting runtime](0010-scripting-runtime.md).
