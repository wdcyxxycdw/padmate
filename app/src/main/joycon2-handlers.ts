import type { JoyCon2Status } from "../shared/joycon2";
import { parseJoyCon2Status } from "../shared/joycon2";
import { ask } from "./ask";
import type { DaemonCall, EditorHandler } from "./library-handlers";
import { validateJoyCon2Serial, validateToggle } from "./security";

/** The daemon's joycon2.* answer, checked field by field. */
function status(value: unknown): JoyCon2Status {
  const checked = parseJoyCon2Status(value);
  if (!checked) throw new Error("Invalid daemon response");
  return checked;
}

/** How long the window that connects a new Joy-Con 2 stays open, in seconds. */
export const joyCon2PairSeconds = 30;

// The Joy-Con 2 section of Settings. The renderer passes no method name and
// no address: the state, the 30 second window that connects and remembers a
// new controller, and forgetting a remembered one by its serial number. The
// failure keeps the daemon's code only (-32027 is "no Joy-Con 2 here").
// joyCon2Watch is the joycon2.changed topic, up while the section is.
export function joyCon2Handlers(
  call: DaemonCall,
  watch: (on: boolean) => Promise<void>,
): Record<string, EditorHandler> {
  return {
    joyCon2Status: {
      count: 0,
      run: () =>
        ask(call, "joycon2.status", {}, status, "Joy-Con 2 state not read"),
    },
    joyCon2Watch: {
      count: 1,
      run: (on) => watch(validateToggle(on)),
    },
    joyCon2Pair: {
      count: 0,
      run: () =>
        ask(
          call,
          "joycon2.pair",
          { seconds: joyCon2PairSeconds },
          status,
          "Joy-Con 2 window not opened",
        ),
    },
    joyCon2CancelPair: {
      count: 0,
      run: () =>
        ask(
          call,
          "joycon2.cancelPair",
          {},
          status,
          "Joy-Con 2 window not closed",
        ),
    },
    joyCon2Forget: {
      count: 1,
      run: (serial) =>
        ask(
          call,
          "joycon2.forget",
          { serial: validateJoyCon2Serial(serial) },
          status,
          "Joy-Con 2 not forgotten",
        ),
    },
  };
}
