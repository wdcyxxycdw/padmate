import {
  parsePadsList,
  parsePadsMigrate,
  parsePadsSet,
} from "../shared/pads-parse";
import { ask } from "./ask";
import type { DaemonCall, EditorHandler } from "./library-handlers";
import { validatePadsSet } from "./security";

function checked<T>(value: T | null): T {
  if (value === null) throw new Error("Invalid daemon response");
  return value;
}

// The settings of each controller (ADR-0017), for Settings → Controllers: the
// controllers that have been connected with their function key, feedback
// switches and emergency combo, one controller's settings written, and the
// retired global members folded into the controllers. The renderer passes no
// method name: the key of a controller it lists and the settings to change,
// cut down to the shapes the daemon takes (validatePadsSet). The failure keeps
// the daemon's code only (-32602 a function key the controller cannot have,
// -32004 a controller it does not know, -32009 a config.json that changed).
export function padHandlers(call: DaemonCall): Record<string, EditorHandler> {
  return {
    padsList: {
      count: 0,
      run: () =>
        ask(
          call,
          "pads.list",
          {},
          (value) => checked(parsePadsList(value)),
          "Controllers not read",
        ),
    },
    padsSet: {
      count: 1,
      run: (params) =>
        ask(
          call,
          "pads.set",
          validatePadsSet(params),
          (value) => checked(parsePadsSet(value)),
          "Controller settings not saved",
        ),
    },
    padsMigrate: {
      count: 0,
      run: () =>
        ask(
          call,
          "pads.migrate",
          {},
          (value) => checked(parsePadsMigrate(value)),
          "Controller settings not moved",
        ),
    },
  };
}
