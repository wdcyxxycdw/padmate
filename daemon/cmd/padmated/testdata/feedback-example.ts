import { host, log, on, pad } from "padmate";

// south: one tick. A preset needs no check: a controller that cannot do it does nothing.
on("south", "press", () => { pad.haptic("tick"); });

// east: 300 ms of rumble, the low-frequency motor at half and the high-frequency one at full; ask host.has first.
on("east", "press", () => {
  if (host.has("rumble")) pad.rumble(0.5, 1, 300);
});

// north: orange while held; on release the layer's color comes back.
on("north", "press", () => { if (host.has("led")) pad.setLed("#FFAA00"); });
on("north", "release", () => { if (host.has("led")) pad.setLed(null); });

// west: read the battery. It is undefined until the controller reports one; when it is low, play the "error" preset.
on("west", "press", () => {
  const battery = pad.info.battery;
  if (!battery) { log.info("battery: not reported"); return; }
  log.info("battery:", battery.state, battery.percent ?? "no percent");
  if (battery.state === "discharging" && battery.percent !== undefined && battery.percent < 20) pad.haptic("error");
});
