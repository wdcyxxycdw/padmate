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
  keys: string[],
  more: Partial<IntegrationFunction> = {},
): IntegrationFunction => ({
  id,
  kind: "command",
  name,
  path,
  keys,
  ...more,
});

/**
 * Two setting areas, as the app's own dialog has them. 主菜单: the groups 编辑
 * (撤销 with Ctrl+Z, 重做 without a key) and 文件 (另存为 with three keys).
 * 工具: the group 笔 that holds G笔 (P; a tool that has the sub-tool 铅笔,
 * also P).
 */
export const demoFunctions: IntegrationFunctions = {
  revision: "f1",
  tree: [
    {
      id: "a:main",
      name: "主菜单",
      children: [
        {
          id: "g:edit",
          name: "编辑",
          children: [
            { id: "n:undo", name: "撤销", function: "cmd:undo" },
            { id: "n:redo", name: "重做", function: "cmd:redo" },
          ],
        },
        {
          id: "g:file",
          name: "文件",
          children: [
            { id: "n:saveas", name: "另存为", function: "cmd:saveas" },
          ],
        },
      ],
    },
    {
      id: "a:tool",
      name: "工具",
      children: [
        {
          id: "g:pen",
          name: "笔",
          children: [
            {
              id: "n:a",
              name: "G笔",
              function: "tool:a",
              children: [{ id: "n:b", name: "铅笔", function: "tool:b" }],
            },
          ],
        },
      ],
    },
  ],
  functions: [
    fn("cmd:undo", "撤销", ["编辑"], ["Ctrl+Z"]),
    fn("cmd:redo", "重做", ["编辑"], []),
    fn(
      "cmd:saveas",
      "另存为",
      ["文件"],
      ["Shift+Alt+S", "Ctrl+Shift+S", "Ctrl+Alt+S"],
    ),
    fn("tool:a", "G笔", ["工具", "笔"], ["P"], {
      kind: "tool",
      cycle: ["tool:b"],
    }),
    fn("tool:b", "铅笔", ["工具", "笔", "G笔"], ["P"], {
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
