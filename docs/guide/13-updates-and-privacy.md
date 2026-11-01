# Updates and privacy

[中文](13-updates-and-privacy.zh-CN.md)

padmate can check for a new version and, with your consent, download and install it. This is the only place where padmate uses the network.

## The network and privacy

- padmate only asks the download site padmate.download for the update information (`latest.yml`) and the installer. If a firewall limits network access, allowing this one domain is enough.
- The download site is hosted on Cloudflare. Cloudflare sees your IP address, a User-Agent with the program name and version, and the requested file name.
- padmate sends no other data: no statistics, and no upload of your profiles, scripts or usage records.

## Checking for updates

On the “Settings” page, “Updates” has two rows:

- “Check for updates automatically”: a switch, on by default. 30 seconds after the window opens, if 24 hours have passed since the last check, it checks once; after that, while padmate has not quit (closing the window is not quitting), it checks every 24 hours. When off, the network is used only when you click “Check for updates”.
- “Manual check”: click “Check for updates” to check now.

When an automatic check fails, nothing is shown and it retries next time. After a manual check, the window shows the result:

- “Up to date”: there is no newer version.
- “Could not check for updates”: the download site could not be reached, or the update information could not be read. Check the network and retry, or click “Download manually” to get the latest installer in a browser.
- “Could not download the update”: the download did not finish, or the file failed the check. Retry later, or click “Download manually”.
- “Could not install the update”: the installer did not start and padmate is still running. Retry later, or click “Download manually”.

## When a new version is found

When a new version is found, the window shows “A new version is available” with the version number and three buttons:

- “Download”: starts downloading the installer. The window shows progress, and you can click “Hide” to do something else meanwhile.
- “Later”: no update this time; the next check reminds you again.
- “Skip this version”: this version is no longer announced automatically, though a newer one still is.

When the download is done, “Download complete” appears. It says mapping stops for a moment during the install and padmate reopens by itself afterwards. It has two buttons:

- “Restart and install”: install now. padmate releases all keys, quits, and reopens by itself when the install is done.
- “Later”: do not install yet. If padmate has not quit, on the “Settings” page click “Check for updates” to see this dialog again.

padmate never installs without asking.

## Stable and preview versions

The download site has stable versions only. A preview version (a version number with a suffix such as `-rc`) receives an update only when a higher stable version exists, for example 1.0.0. Download preview versions from [GitHub Releases](https://github.com/wdcyxxycdw/padmate/releases).

## Security

- The downloaded installer must pass the SHA-512 check against `latest.yml`, or it is not installed. Both are downloaded from the download site over HTTPS.

## Uninstalling

Uninstall padmate under “Installed apps” in the Windows settings. Along the way the uninstaller asks “Keep your profiles and scripts?”: click “Yes” to keep the profiles, scripts and settings in `%APPDATA%\padmate`, so a later reinstall can continue with them; click “No” to delete that whole folder.
