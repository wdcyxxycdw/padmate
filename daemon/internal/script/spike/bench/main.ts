// The SPK-03 benchmark script: SCRIPTING §8.1 and §8.2 reduced to the M0d
// padmate stub (on, key, sleep), plus an async handler and an empty one.
// The spike host in the parent directory runs it; see its package doc.
import { on } from "padmate";
import "./async";
import "./gestures";
import "./stick";

// An empty handler: what the host costs when the script does nothing.
on("misc1", "press", () => {});
