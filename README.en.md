# padmate

[中文](README.md)

padmate maps common game controllers to keyboard shortcuts. It supports a customizable function key, stick knobs and advanced scripting, and works with Xbox, PlayStation and Joy-Con controllers. It suits work that relies on many shortcuts, such as painting, video editing and programming.

**Turn an idle controller into a productivity tool.**

For Windows 10 and 11 (x64).

## Features

- **Button mapping**: Each button can have separate mappings for a tap, a hold and a double tap. Keyboard keys, the mouse and simple macros are supported.
- **Function key**: Combine the function key with another button to give the controller more shortcuts.
- **Sticks and triggers**: A stick can act as a knob or a mouse; a trigger works as a button and can have two stages.
- **Profiles per app**: Each app can have its own profile, which switches automatically with the foreground app.
- **Scripts**: Write scripts in TypeScript or JavaScript for more complex actions.
- **AntiMicroX compatibility**: AntiMicroX profiles can be imported.

## Install

1. Download the latest version from the GitHub [Releases](https://github.com/wdcyxxycdw/padmate/releases) page, or from the dedicated download site [padmate.download](https://padmate.download/)
2. Run the downloaded installer. The installer is not code-signed yet. If Windows stops the installation, click “More info”, then “Run anyway” to continue.

## User guide

The user guide comes with the installer, in the `resources\docs\guide` subfolder of the installation folder. Start reading at `00-index.en.md` (the Chinese version is `00-index.md`). The default installation folder is `%LOCALAPPDATA%\Programs\padmate`.

## ToDo

- [x] Stick knob, arrow key and mouse modes
- [x] Automatic profile switching
- [x] Custom scripts
- [x] AntiMicroX profile compatibility
- [ ] Support for Switch 2 Joy-Con controllers
- [ ] Support for macOS and Linux
- [ ] Ready-made profiles for more apps

## Privacy

padmate goes online only to check for and download updates, and collects no user information. It also works without a network connection.

## Build from source

Requires Go 1.27, Node 24, [Task](https://taskfile.dev) and golangci-lint v2.13. Task and golangci-lint can be installed with Go; afterwards, make sure `%USERPROFILE%\go\bin` is in your PATH:

```bash
go install github.com/go-task/task/v3/cmd/task@latest
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
```

Then run:

```bash
task sdl
task app:install
task ci
task build
```

This repository contains the code only and cannot build the installer. Download the installer from GitHub Releases or padmate.download.

## License

This project is released under the [MIT](LICENSE) License.
