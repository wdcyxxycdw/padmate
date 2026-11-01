# Layers and the function key

[中文](03-layers-and-function-keys.zh-CN.md)

A controller has few keys. The function key and layers let one key do different things at different times. The function key is set in the app; layers can only be written in the profile file.

## The function key

Of the two keys in the middle of the controller, View and Menu, one is the function key, and by default it is Menu. On a PlayStation controller they are Create (or Share) and Options, and on a Switch Pro controller − and +.

The function key works like a laptop's Fn key:

- You cannot set a shortcut on it, and it sends nothing itself. On the controller picture its tag says “Function key”.
- While you hold it, the other keys switch to another set of settings. A key with nothing set in this set uses its “Normal” setting as usual.
- Which set applies is decided the moment you press a key. A key already held before you pressed the function key is not affected.

### Changing the function key

Click “Settings”, open “General”, find “Function key”, and click one of the keys. It applies at once, for all profiles and controllers.

### Why the Xbox button cannot be used

The Xbox button in the middle (the PS button on a PlayStation controller, Home on a Switch controller) cannot be the function key and cannot have a shortcut, and the controller picture has no tag for it. Other programs, and the controller itself, already use it:

- Pressing it opens the Xbox Game Bar of Windows.
- While Steam runs, pressing it opens Steam's Big Picture mode, and pressing it with Menu becomes Alt+Tab.
- On some controllers, holding it turns the controller off, and holding it while pressing another key resets the controller's settings.

padmate cannot stop any of this, so it does not use the button: it cannot be mapped, used in a script, or put in the emergency pause combination. padmate still reads it, and the diagnostics page shows when it is pressed.

If you chose the Xbox button as the function key before, Menu is used now. The next time you change anything on the Settings page, the old choice is removed from the configuration file.

### Setting the function key's set

1. At the top of the “Mapping” page is the switch “Normal / Hold function key”. Click “Hold function key”. The controller picture and key settings then show the set used while you hold the function key.
2. Click the tag of a key and capture a shortcut as usual.
3. A key with nothing set in this set has a gray tag that says “Normal: …”. That is what it sends without the function key.
4. To put a key back to its normal setting: open it, click “Key actions”, and choose “Clear in this set”.
5. Click “Normal” to go back to the normal settings.

## Layers

A layer is a whole set of key settings, like sheets of paper laid on top of each other. To find a key's setting, padmate looks from the top layer down and uses the first it finds.

The app edits one set of keys per profile, the base layer. Other layers can only be written in the profile file; the app keeps them unchanged when it saves.

### Writing a layer

Each profile has its own folder in `%APPDATA%\padmate\profiles`, and the `profile.json` in it is the profile file. The profile below has two layers: while you hold the left shoulder button, the A key sends redo; after you let go, it is undo again.

```json
{
  "schemaVersion": 1,
  "name": "layers",
  "layers": {
    "base": {
      "leftshoulder": { "press": { "type": "layer", "layer": "alt", "op": "hold" } },
      "south": { "press": { "type": "keys", "keys": "Ctrl+Z", "mode": "tap" } }
    },
    "alt": {
      "south": { "press": { "type": "keys", "keys": "Ctrl+Shift+Z", "mode": "tap" } }
    }
  }
}
```

- Keys are written by control name, for example `south` (the bottom face button) and `leftshoulder` (the left shoulder button). All names are in `resources\schema\profile.schema.json` in the install folder.
- The `op` of a `layer` action has four values: `hold` opens the layer while held; `toggle` opens it on one press and closes it on the next; `switch` replaces the base layer with another; `cycle` goes round the layers listed in `layers`.
- A key with nothing set in a layer is looked up further down: base first, `common` last. `common` holds the keys shared by all layers.
- To make a key do nothing in a layer, write `"block": true`.
- `defaultLayer` is the base layer at start-up. Without it, it is `base`.

View and Menu are `back` and `start`. A setting written on the current function key does not apply, but stays in the file. The Xbox button is `guide`: no setting written on it applies, and the check gives a warning for each; when you edit the profile on the Mapping page, they are removed from the file. The function key's set is written in `functionSet`, in the same way as a layer.

After you save the file, padmate rereads it. To check it, run `.\padmatectl.exe validate profile.json` in the `resources\bin` folder. It points out what is wrong and does not change the file.
