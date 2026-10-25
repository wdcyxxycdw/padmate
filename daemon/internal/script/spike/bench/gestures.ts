// SCRIPTING §8.2, TABMATE-style gestures and layers.
import { key, on } from "padmate";
import { layer, layers } from "./layers";
import type { InputEvent } from "./types";

on("south", "press", () => key.tap("Ctrl+Z"), { label: "Undo" });
on("back", "tap", () => key.tap("F19"), { label: "Layers panel" });
on("back", "doubleTap", () => layers.toggle("alt"), { label: "Alt layer" });
on("west", "tap", () => key.tap("F13"), { label: "Pen" });
on("west", "hold", (e: InputEvent) => e.holdKeys("F14"), { label: "Eraser while held" });
on("rightshoulder", "press", (e: InputEvent) => e.holdLayer("alt"));
layer("alt").on("south", "press", () => key.tap("F15"), { label: "Redo" });
