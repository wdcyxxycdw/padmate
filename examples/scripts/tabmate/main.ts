import { key, layer, layers, on } from "padmate";

on("south", "press", () => { void key.tap("Ctrl+Z"); }, { label: "撤销" });
on("north", "tap", () => { void key.tap("F19"); }, {
  label: "切换图层面板",
  multiTapMs: 220,
});
on("north", "doubleTap", () => layers.toggle("alt"), {
  label: "Alt 层",
  multiTapMs: 220,
});
on("west", "tap", () => { void key.tap("F13"); }, {
  label: "笔",
  holdMs: 240,
});
on("west", "hold", (event) => event.holdKeys("F14"), {
  label: "临时橡皮",
  holdMs: 240,
});
on("rightshoulder", "press", (event) => event.holdLayer("alt"), {
  label: "临时 Alt 层",
});
layer("alt").on("south", "press", () => { void key.tap("F15"); }, {
  label: "重做",
});
