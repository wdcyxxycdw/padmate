# ADR-0018: Installer and updates

[中文](0018-installer-and-updates.zh-CN.md)

Related: [ARCHITECTURE §15](../ARCHITECTURE.md); [ADR: Processes and stack](0001-processes-and-stack.md)

## Context

An ordinary user downloads one installer, edits no file, and within minutes gets the first shortcut to the painting app. The constraints:

- Installing, upgrading and uninstalling must stop the running daemon without leaving a key stuck, and must not kill another padmate by mistake (a development build, a portable copy).
- Uninstalling deletes the whole installation folder, so the installation folder must never be a folder that holds other things.
- Testing installation, upgrades and uninstallation must not touch the machine's real state.
- Most users are in mainland China, where GitHub cannot be reached.

## Decision

### 1. PE resources and the portable package
After compiling, the pure-Go `winres` writes the icon and the version resources; no `.syso` is generated in the source tree, and `rc.exe` is not used (it needs another toolchain). The portable package is not a full portable mode: logs and state still use the user's data folder.

### 2. The installer (an NSIS wizard)
- electron-builder builds an NSIS package. **It installs for the current user only and never elevates**: the manifest is `asInvoker`, there is no "all users" option, and `packageinstaller verify` checks the manifest.
- **The uninstall entry's GUID is fixed and never changes**; with another one, an old installation would no longer be upgraded as the same program.
- The desktop shortcut is created only on a fresh install and on a manual reinstall, never by an automatic update, so a shortcut the user deleted does not come back with each update.
- **The installation folder.** If the last part of the chosen folder is `padmate`, that is the installation folder; otherwise padmate goes into a `padmate` subfolder of it. When the subfolder was added, the page stays so the user can confirm again; where it goes is always visible before installing. `/D=` of a silent install is used as given.
- **The folder guard.** The folder must be empty, already hold `padmate.exe` or the uninstaller, or be the installation folder recorded in the registry. A folder with other files is left alone.
- The installer tries a write first, because electron-builder deletes the old version before writing the new one, and a failed write would leave the user with neither version.
- An upgrade into another folder stops the daemon in both the new and the old folder, then has the old uninstaller remove the old folder with `--updated`; the user's data is left alone.

### 3. Stopping the daemon
- The parameters of the IPC call `daemon.exit` must be exactly `{reason:"installer"}`, and it follows the sequence of the tray's "Exit": first pause mapping and release every key. It is not on the UI's allow-list, so the UI cannot make the daemon exit. `padmatectl quit [--under DIR]` asks it to exit and waits for the process to go.
- **The installer's order:** first `quit`; only if quit fails, `release-all` (it makes every `--inject` padmated of the user release its keys and destroy its scripts, including ones installed elsewhere); then wait 10 seconds; only what is still there gets `TerminateProcess`. Before ending a `padmated.exe`, `release-all` runs once if it has not already, because ending the daemon does not release held keys.
- **Processes are found by image path, not by name.** Programs with the same name outside the installation folder are never touched. taskkill, PowerShell and wmic are not used.

### 4. Uninstalling
Cleaning up happens only on a full uninstall; an upgrade leaves the user's data alone. **It asks whether to keep the profiles and scripts**, keeping them by default, since they are the user's work and deleting them cannot be undone; a silent uninstall does not ask. The start-at-sign-in entry is deleted only when Run points into this installation folder.

### 5. Test isolation
The smoke test uses `PADMATE_INSTALLER_TEST_ROOT` to replace `%APPDATA%`, `%LOCALAPPDATA%` and the Run value with test folders; `quit` uses a separate IPC folder, and `release-all` is not run. The installation folder, the uninstall entry and the shortcuts are still real, and are deleted at the end; it refuses to run on a machine where padmate is already installed.

### 6. Signing
Private keys and passwords never pass through AI, and the repository holds no certificate. **Builds are not signed by default**; without a certificate releases go out as they are, and the documentation describes SmartScreen's warning. With a certificate, `app-update.yml` must carry `publisherName`, or electron-updater does not verify the signature.

### 7. Antivirus false positives
The low-level keyboard hook is on only while padmatectl "records a shortcut", for at most 30 seconds, and only watches without swallowing. The offline PE check (`task pe:check`) requires the import table and the API list to equal the committed lists, so a new Windows API shows up in review; injection-type APIs are never allowed. Nothing is scanned automatically or uploaded, since cloud services may upload samples automatically.

### 8. The download site and automatic updates
- `https://padmate.download/` is a custom domain of a Cloudflare R2 bucket holding the installers, `.sha256` files and `latest.yml`, **stable releases only**. There is no web page: a Worker that routes only `/` reads `latest.yml` and redirects with 302 to the newest installer. Its route cannot be `/*`, or it would stand in front of updates.
- Updates use electron-updater, with the URL written in the code; `channel` must be set, or an rc build would read `rc.yml`.
- **Downloading and installing happen only with the user's consent**: no automatic download, no automatic install on exit (which would quietly stop mapping while painting), no differential updates. It checks every 24 hours; the setting is stored in the UI's own folder, not in config.json.
- On failure it offers a "Download manually" link, since a browser can resume a download and electron-updater cannot (the installer is about 135 MB).
- **Privacy:** only `latest.yml` and the installer are requested. Cloudflare sees the IP, `User-Agent: padmate/<version>` and the file name.
- **The root of trust:** an unsigned build relies only on the SHA-512 in `latest.yml`, which comes from the download site along with the installer, so the root of trust is whoever can write to the bucket or the domain.

### 9. Releases
A person runs `task release:r2`; builds never upload, and the task reads no credentials, using the wrangler already signed in on the release machine. Before uploading, it refuses prerelease tags, a `latest.yml` that does not match, an installer published before, and a version not newer than the site's. **The order: the installer, the `.sha256`, and only then `latest.yml`**, since clients and the Worker follow it. GitHub releases stay on the public repository, and previews are published only there.

## Costs

- An unsigned installer triggers SmartScreen's "Unknown publisher", and every version builds reputation from zero.
- The wizard takes one more click than a one-click installer.
- Cloudflare has no guarantees on networks in mainland China. Its security features may challenge an unusual User-Agent, which shows as "could not check for updates"; `/latest.yml` and the installers need skip rules.
- A published installer cannot be overwritten; a fix means a new version.

## Alternatives given up

- **Using the chosen folder directly as the installation folder, or dropping the guard and relying on the subfolder.** Choosing the desktop would mean deleting the desktop on uninstall; users could also choose a `padmate` folder that already holds other things.
- **No trial write, with the installation reporting the error when it fails.** The user would be left with neither the old version nor the new one.
- **Installing updates automatically when the UI closes, or differential updates.** The first stops mapping while painting; the second has to keep a 135 MB copy.
- **A PE check of the import table only.** Go executables load APIs at run time, which the import table does not show.
- **A download page with a web page, or another server as the download source.** The first is too verbose, the second too slow.
- **Ending processes by name with `taskkill`, PowerShell or wmic, or always running `release-all` after quit.** It would kill development builds and portable copies by mistake, or make padmated elsewhere release keys and destroy scripts.
