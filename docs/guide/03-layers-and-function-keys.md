# Layers and the function key

[中文](03-layers-and-function-keys.zh-CN.md)

A controller has few keys. The function key and layers let one key do different things at different times. The function key is set in the app; layers can only be written in the profile file.

## The function key

The middle of the controller has three keys: View, Menu and the Xbox button. On a PlayStation controller they are Create (or Share), Options and the PS button, and on a Switch Pro controller they are −, + and Home. One of them is the function key, and by default it is Menu.

The function key works like a laptop's Fn key:

- You cannot set a shortcut on it, and it sends nothing itself. On the controller picture its tag says “Function key”.
- While you hold it, the other keys switch to another set of settings. A key with nothing set in this set uses its “Normal” setting as usual.
- Which set applies is decided the moment you press a key. A key already held before you pressed the function key is not affected.

### Changing the function key

1. Click “Settings”, open “General”, find “Function key”, and click one of the keys. It applies at once, for all profiles and controllers.
2. Choosing the Xbox button shows a dialog. Pressing it also opens the Xbox Game Bar of Windows, which padmate cannot stop. In Windows, under “Settings → Gaming → Game Bar”, turn off “Allow your controller to open Game Bar” (older versions of Windows call it “Open Xbox Game Bar using this button on a controller”). Click “Open Windows Settings” to go straight to that page, and “Got it” to close the dialog. Your choice already applies. Steam may also take the Xbox button while it runs.

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

View, Menu and the Xbox button are `back`, `start` and `guide`. A setting written on the current function key does not apply, but stays in the file. The function key's set is written in `functionSet`, in the same way as a layer.

After you save the file, padmate rereads it. To check it, run `.\padmatectl.exe validate profile.json` in the `resources\bin` folder. It points out what is wrong and does not change the file.
