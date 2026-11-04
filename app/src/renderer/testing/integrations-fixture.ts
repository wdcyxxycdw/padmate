// An app integration for tests, shaped the way the daemon reports one. The app
// is made up: nothing in the window names a real program, so the tests do not
// either.
import type { PadmateAPI } from "../../shared/api";
import type {
  IntegrationFunction,
  IntegrationFunctions,
  IntegrationInfo,
  IntegrationItem,
  IntegrationItems,
} from "../../shared/integrations";
import { createPadmateStore, type PadmateStore } from "../state/store";
import type { editorAPIFixture } from "./editor-api-fixture";

export const demoApp: IntegrationInfo = {
  id: "demoapp",
  name: "Demo Paint",
  exe: ["DemoPaint.exe"],
  found: true,
  dataDir: "C:\\Users\\me\\DemoPaint",
  running: false,
  profiles: ["paint"],
};

const fn = (
  id: string,
  name: string,
  path: string[],
  category: string,
  keys: string[],
  more: Partial<IntegrationFunction> = {},
): IntegrationFunction => ({
  id,
  kind: "command",
  name,
  path,
  category,
  keys,
  ...more,
});

export const demoFunctions: IntegrationFunctions = {
  revision: "f1",
  categories: [
    { id: "edit", name: "编辑" },
    { id: "tool", name: "工具" },
  ],
  functions: [
    fn("cmd:undo", "撤销", ["编辑"], "edit", ["Ctrl+Z"]),
    fn("cmd:redo", "重做", ["编辑"], "edit", []),
    fn("tool:a", "G笔", ["工具", "笔"], "tool", ["P"], {
      kind: "tool",
      cycle: ["tool:b"],
    }),
    fn("tool:b", "铅笔", ["工具", "笔"], "tool", ["P"], {
      kind: "tool",
      cycle: ["tool:a"],
    }),
  ],
};

export const undoItem: IntegrationItem = {
  keys: "Ctrl+Z",
  function: "cmd:undo",
  name: "撤销",
  path: ["编辑"],
};

export function consistentItems(
  items: IntegrationItem[] = [],
): IntegrationItems {
  return {
    revision: "i1",
    items,
    statuses: items.map((item) => ({
      keys: item.keys,
      function: item.function,
      state: "consistent" as const,
    })),
    consistent: true,
    dismissed: false,
  };
}

type Api = ReturnType<typeof editorAPIFixture>;

/**
 * A store on the fixture's api that is connected, with the integrations the
 * daemon would list and the default answers for the other methods.
 */
export function integrationStore(
  api: Api,
  integrations: IntegrationInfo[] = [demoApp],
): PadmateStore {
  api.listIntegrations.mockImplementation(async () => integrations);
  api.getIntegrationFunctions.mockImplementation(async () => demoFunctions);
  api.getIntegrationItems.mockImplementation(async () => consistentItems());
  const store = createPadmateStore(api as unknown as PadmateAPI);
  store.setState({
    connection: { phase: "connected" },
    integrations,
  } as never);
  return store;
}
