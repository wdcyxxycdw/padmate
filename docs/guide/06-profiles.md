# Profiles and switching by app

[中文](06-profiles.zh-CN.md)

A profile is a whole set of key settings. You can give each app its own profile; padmate switches automatically to follow the app in front.

## The Profiles page

Click “Profiles” on the left to open the Profiles page.

- At the top is the “Default” profile, marked “Used by apps without a binding”. It cannot be deleted or renamed, but you can change its keys.
- padmate comes with a profile named CSP for CLIP STUDIO PAINT, bound to it. It is added once, when padmate starts, and only when you have no profile of that name; if you delete it or its binding, it does not come back. Its function names are in Chinese.
- Every other profile has a card listing, as icons, the apps it is used for. The one in use says “In use”.
- Click the pencil button “Edit keys” to edit that profile on the “Mapping” page. Click “Duplicate” to make a copy; changing it does not affect the original, but it has no bound apps.
- The menu of a card has “Export…”, “Profile settings…”, “Rename…” and “Delete…”. At the top of the page are “Import…” and “New profile”.
- A card whose profile file has errors says “The profile file has errors” and cannot be opened.

For “New profile”, write the “Name” and click “Create” to get an empty profile; the name cannot match another profile. “Delete…” deletes the whole profile, scripts included, and cannot be undone.

## Switching by app

“Switch profiles by app” at the top of the page is on by default. When it is on, padmate switches to the profile bound to the app in front. Apps without a binding use the “Default” profile.

- On a profile card, click “Add app” and pick an app in one of three ways: “Pick a window…” (you have 30 seconds to click the target window), “Choose from installed apps…” and “Browse…”.
- An app is recognised by its program's file name, not its window title. Different programs with the same name use the same profile.
- Click the icon of an app on a card for “Turn off” (later “Turn on”) and “Unbind”. “Turn off” disables the binding for now but keeps it.
- An app can be bound to only one profile. When it is already bound elsewhere, padmate asks whether to “Keep it” or “Change”.
- An app that runs as administrator can be bound too, but Windows may block the shortcuts sent to it.

## Importing and exporting

- “Export…” saves a profile as a `.padmate` file, with the scripts it uses inside.
- “Import…” reads such a file, one profile per file, at most 4 MiB. AntiMicroX profiles are imported with it too; see [Importing from AntiMicroX](10-import-antimicrox.md).
- When a profile with the same name exists, padmate asks what to do: “Replace” overwrites everything in that profile, and “Save as new” saves under a new name.
- Imported scripts run directly. Import only files you trust.
