# padmate

[中文](README.md)

padmate turns a game controller into a console of keyboard shortcuts. While you paint, you hold the controller in your left hand, and its buttons, sticks and triggers send the shortcuts you use in your painting app.

For Windows 10 and 11 (x64). MIT license.

## Features

- Each button can have its own tap, hold and double tap, as well as sequences sent in order.
- Sticks and triggers can act as arrow keys, a knob or a mouse, and a trigger can have two stages.
- While the function key is held (Menu by default, or View or the Xbox button, as you choose in the settings), the other buttons switch to another set of shortcuts.
- One profile per app, switched automatically by the app in front.
- TypeScript scripts (macros) handle more complex actions.
- The Diagnostics page lists what each press sent, and says why when nothing happened.
- AntiMicroX profiles can be imported, and for a controller that SDL does not know, the mapping wizard builds a mapping.
- No stuck keys: on exit, pause, lock screen and when a controller disconnects, every held key is released.

## Install

1. Download the latest `padmate-vX.Y.Z-windows-amd64-setup.exe` from [Releases](https://github.com/wdcyxxycdw/padmate/releases).
2. Run it. It installs for the current user and needs no administrator rights.
3. The installer is not code-signed. When Windows says “Windows protected your PC”, click “More info”, then “Run anyway”.

To upgrade, run the new installer. Your profiles and settings are kept.

## Usage

1. Connect the controller and open padmate.
2. On the “Mapping” page, click a button on the picture of the controller.
3. In the drawer that opens, click “Capture shortcut” and press the shortcut you use in your painting app.
4. Switch to your painting app and press that button on the controller.
5. For a set of buttons of its own for one app: on the “Profiles” page, click “New profile”, then “Add app”.

Emergency pause: hold View and Menu together for 2 seconds. All mapping pauses and every held key is released. Hold them for 2 seconds again to resume.

The app starts in Chinese. To switch to English, open “Settings”, and under “General” change “Language”.

## User guide

The user guide comes with the installer, in the `resources\docs\guide` folder of the install folder. Start with `00-index.en.md`; the Chinese version is `00-index.md`. The install folder is `%LOCALAPPDATA%\Programs\padmate` by default.

## Privacy

padmate goes online only to check for updates. It asks GitHub for release information and sends nothing else. In “Settings”, with “Check for updates automatically” turned off, it goes online only when you click “Check for updates”.

## Building from source

You need Go 1.27 (the releases are built with it; `daemon/go.mod` asks for 1.26 at least), Node 24, [Task](https://taskfile.dev) and golangci-lint v2.13 (the checks of `task ci` use it). Install Task and golangci-lint with Go, and make sure `%USERPROFILE%\go\bin` is in your PATH afterwards:

```bash
go install github.com/go-task/task/v3/cmd/task@latest
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
```

Then:

```bash
task sdl
task app:install
task ci
task build
```

`task sdl` downloads the pinned SDL3.dll, `task app:install` installs the dependencies of the window, `task ci` runs all the checks, and `task build` builds padmated, padmatectl and padmate-probe into `bin/`.

This repository has the code only. The installer is built from another, development repository, so the packaging tasks do not work here; download the installer from Releases. The tests that check the user documents are skipped here.

## License

[MIT](LICENSE). This project contains no code from AntiMicroX (GPL-3.0).
