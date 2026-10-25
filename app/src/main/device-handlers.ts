import type {
  DeviceIdentify,
  LatencyDevice,
  LatencyResult,
  MappingResult,
  MappingWarning,
} from "../shared/api";
import { ask, count, record } from "./ask";
import type { DaemonCall, EditorHandler } from "./library-handlers";
import { RawWatch } from "./raw-watch";
import { validateDevice, validateDeviceKey, validateMapping } from "./security";

/** The daemon's devices.identify result, checked field by field. */
function identifyResult(value: unknown): DeviceIdentify {
  if (value === null || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid daemon response");
  const cues = value as Record<string, unknown>;
  if (
    typeof cues.rumble !== "boolean" ||
    typeof cues.led !== "boolean" ||
    typeof cues.player !== "boolean"
  )
    throw new Error("Invalid daemon response");
  return { rumble: cues.rumble, led: cues.led, player: cues.player };
}

/** One controller of the daemon's latency.get result, checked field by field. */
function latencyDevice(value: unknown): LatencyDevice {
  const pad = record(value);
  if (
    typeof pad.device !== "string" ||
    typeof pad.name !== "string" ||
    typeof pad.warn !== "boolean"
  )
    throw new Error("Invalid daemon response");
  return {
    device: pad.device,
    id: count(pad.id),
    name: pad.name,
    samples: count(pad.samples),
    p50: count(pad.p50),
    p90: count(pad.p90),
    p99: count(pad.p99),
    max: count(pad.max),
    warn: pad.warn,
  };
}

/** The daemon's latency.get result, checked field by field. */
function latencyResult(value: unknown): LatencyResult {
  const result = record(value);
  if (!Array.isArray(result.devices) || typeof result.dryRun !== "boolean")
    throw new Error("Invalid daemon response");
  return {
    devices: result.devices.map(latencyDevice),
    dryRun: result.dryRun,
    warnP99Us: count(result.warnP99Us),
    warnMinSamples: count(result.warnMinSamples),
  };
}

function strings(value: unknown): string[] {
  if (!Array.isArray(value)) throw new Error("Invalid daemon response");
  return value.map((item) => {
    if (typeof item !== "string") throw new Error("Invalid daemon response");
    return item;
  });
}

function mappingWarning(value: unknown): MappingWarning {
  const warning = record(value);
  if (typeof warning.code !== "string")
    throw new Error("Invalid daemon response");
  return {
    code: warning.code,
    ...(warning.device === undefined ? {} : { device: count(warning.device) }),
  };
}

/** The daemon's devices.setMapping and devices.resetMapping answer, checked. */
function mappingResult(value: unknown): MappingResult {
  const result = record(value);
  if (
    typeof result.device !== "string" ||
    typeof result.guid !== "string" ||
    typeof result.mapping !== "string" ||
    typeof result.changed !== "boolean" ||
    typeof result.applied !== "boolean" ||
    (result.problem !== undefined && typeof result.problem !== "string") ||
    (result.revision !== undefined && typeof result.revision !== "string") ||
    !Array.isArray(result.warnings)
  )
    throw new Error("Invalid daemon response");
  return {
    device: result.device,
    guid: result.guid,
    mapping: result.mapping,
    keys: strings(result.keys),
    changed: result.changed,
    applied: result.applied,
    ...(result.problem === undefined ? {} : { problem: result.problem }),
    ...(result.revision === undefined ? {} : { revision: result.revision }),
    notes: strings(result.notes),
    warnings: result.warnings.map(mappingWarning),
  };
}

// Identify a controller (IN-13). The renderer passes the key of a controller
// it lists; the daemon makes it buzz and flash and says which cues it can
// give. Nothing but the key goes to the daemon.
//
// The latency figures of the diagnostics page (DMN-15, UI-22): read once a
// second while the page is open, and cleared on its button. The renderer
// passes at most a selector (a connection ID or a key), nothing else.
//
// The mapping wizard and custom mappings (IN-17, IN-18, IN-19, ADR-0072): the
// raw events of one controller (rawWatchStart, rawWatchStop; raw owns the
// input.raw subscription), and the SDL mapping string saved or taken away for
// it. The renderer passes a selector and a checked mapping string; the daemon
// parses and normalises the mapping and checks its GUID.
export function deviceHandlers(
  call: DaemonCall,
  raw: RawWatch = new RawWatch(call, { up: async () => {} }),
): Record<string, EditorHandler> {
  return {
    rawWatchStart: {
      count: 1,
      run: (device) => raw.start(validateDeviceKey(device)),
    },
    rawWatchStop: {
      count: 1,
      run: (device) => raw.stop(validateDeviceKey(device)),
    },
    mappingSet: {
      count: 2,
      run: (device, mapping) =>
        ask(
          call,
          "devices.setMapping",
          {
            device: validateDeviceKey(device),
            mapping: validateMapping(mapping),
          },
          mappingResult,
          "Mapping not saved",
        ),
    },
    mappingReset: {
      count: 1,
      run: (device) =>
        ask(
          call,
          "devices.resetMapping",
          { device: validateDeviceKey(device) },
          mappingResult,
          "Mapping not reset",
        ),
    },
    latencyGet: {
      count: 1,
      run: (device) => {
        const selector = validateDevice(device);
        return ask(
          call,
          "latency.get",
          selector ? { device: selector } : {},
          latencyResult,
          "Latency not read",
        );
      },
    },
    latencyReset: {
      count: 1,
      run: (device) => {
        const selector = validateDevice(device);
        return ask(
          call,
          "latency.reset",
          selector ? { device: selector } : {},
          (value) => {
            const answer = record(value);
            if (typeof answer.reset !== "boolean")
              throw new Error("Invalid daemon response");
            return { reset: answer.reset };
          },
          "Latency not cleared",
        );
      },
    },
    identifyDevice: {
      count: 1,
      run: async (key) => {
        const device = validateDeviceKey(key);
        const pending = call("devices.identify", { device });
        if (!pending) throw new Error("Daemon is not connected");
        try {
          return identifyResult(await pending);
        } catch (error) {
          // The code says what went wrong (-32004: the controller is gone).
          // The message of a failing controller is the daemon's, for its log.
          const e = error as { code?: unknown };
          throw Object.assign(new Error("Controller not identified"), {
            ...(typeof e?.code === "number" ? { code: e.code } : {}),
          });
        }
      },
    },
  };
}
