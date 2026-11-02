# Layers and the function key

[中文](03-layers-and-function-keys.zh-CN.md)

A controller has few keys. The function key and layers let one key do different things at different times. The function key is set in the app; layers can only be written in the profile file.

## The function key

Each controller has its own function key. Which key it is depends on the kind of controller:

| Controller | Default function key | Can also be |
|---|---|---|
| Xbox, PlayStation, Switch Pro and the like | Menu | View |
| A single Joy-Con, the left one | ZL | L |
| A single Joy-Con, the right one | ZR | R |
| A left and a right Joy-Con connected as one controller | − | + |

On a PlayStation controller, View and Menu are Create (or Share) and Options, and on a Switch Pro controller − and +. A single Joy-Con uses ZL or ZR by default because those keys are large and easy to press. − and + are not offered on a single Joy-Con, because you cannot reach them while you hold it, and neither can SL or SR be the function key. The Xbox, PS and Home buttons cannot be the function key.

The function key works like a laptop's Fn key:

- You cannot set a shortcut on it, and it sends nothing itself. On the controller picture its tag says “Function key”. In the “Trigger settings”, “Press” sets how far ZL or ZR must be pulled to count as held when it is the function key.
- While you hold it, the other keys switch to another set of settings. A key with nothing set in this set uses its “Normal” setting as usual.
- Which set applies is decided the moment you press a key. A key already held before you pressed the function key is not affected.

### Changing the function key

Click “Settings”. In “General”, find the row “Controllers” and click “Open”. The page has a section for each controller, and under each one its “Function key”: click one of the keys. It applies at once, to that controller only, for all profiles. When you change it, the keys that controller is holding are released first.

Controllers that are connected now come first. Controllers that were connected before and are not now are under “Previously connected”; you can change them too, and they use the new setting the next time they connect.

On the “Mapping” page, the set for “Hold function key” uses the function key of the controller the page is showing. Switch to another controller and the function key switches with it.

If `%APPDATA%\padmate\config.json` still has a global function key, feedback setting or emergency pause, a controller with no settings of its own uses them. The global function key applies only to Xbox, PlayStation, Switch Pro and similar controllers, and a Joy-Con uses its own default; the global feedback applies to every controller; the global emergency pause combination is taken by the kind of controller. The first time you change anything in “Controllers”, padmate first writes these values under every controller it remembers and then removes them from the settings file, so every controller behaves as before. A controller that is not on record uses the default of its kind.

### Why the Xbox button cannot be used

The Xbox button in the middle (the PS button on a PlayStation controller, Home on a Switch controller) cannot be the function key and cannot have a shortcut, and the controller picture has no tag for it. Other programs, and the controller itself, already use it:

- Pressing it opens the Xbox Game Bar of Windows.
- While Steam runs, pressing it opens Steam's Big Picture mode, and pressing it with Menu becomes Alt+Tab.
- On some controllers, holding it turns the controller off, and holding it while pressing another key resets the controller's settings.

padmate cannot stop any of this, so it does not use the button: it cannot be mapped, used in a script, or put in the emergency pause combination. padmate still reads it, and the diagnostics page shows when it is pressed.

If the global function key in the settings file is the Xbox button, Menu is used instead, and it is removed from the file the first time you change anything in “Controllers”.

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

View and Menu are `back` and `start`. On a single Joy-Con, ZL, L, ZR and R are `lt`, `leftshoulder`, `rt` and `rightshoulder`. A setting written on the function key of a controller does not apply on that controller, but stays in the file. The Xbox button is `guide`: no setting written on it applies, and the check gives a warning for each; when you edit the profile on the Mapping page, they are removed from the file. The function key's set is written in `functionSet`, in the same way as a layer.

After you save the file, padmate rereads it. To check it, run `.\padmatectl.exe validate profile.json` in the `resources\bin` folder. It points out what is wrong and does not change the file. By default it checks with Menu as the function key; to check with another key, add `--function-key` with `start`, `back`, `leftshoulder`, `rightshoulder`, `lt` or `rt`.
