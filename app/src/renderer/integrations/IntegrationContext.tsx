// What every part of the app integrations UI shares (ARCHITECTURE §16.5): the
// store, the blocking "close the app first" dialog that comes before anything
// that writes the app's settings, and the way a write is made and its failures
// are said. Without a provider (a test of another part of the window) no
// integration shows anywhere.
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useStore } from "zustand";
import type {
  IntegrationChange,
  IntegrationInfo,
  IntegrationWritten,
} from "../../shared/integrations";
import {
  boundIntegrations,
  INTEGRATION_FILES_BUSY,
  INTEGRATION_REVISION,
  INTEGRATION_RUNNING,
  INTEGRATION_UNAVAILABLE,
} from "../../shared/integrations";
import type { Language, PadmateStore } from "../state/store";
import { Dialog } from "../ui/controls";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

export type WriteOutcome =
  | { ok: true; written: IntegrationWritten }
  | { ok: false; cancelled: true }
  | { ok: false; cancelled: false; message: string };

export interface IntegrationHost {
  store: PadmateStore;
  /**
   * Resolves true when the app is not running. While it runs a blocking
   * dialog asks to close it, with 重试 and 取消; resolves false on 取消.
   */
  requireClosed(info: IntegrationInfo): Promise<boolean>;
  /** Writes the app's settings after the running check; see WriteOutcome. */
  write(
    info: IntegrationInfo,
    changes: IntegrationChange[],
  ): Promise<WriteOutcome>;
}

export const IntegrationContext = createContext<IntegrationHost | null>(null);

/** The host of the integrations UI, or null outside a provider. */
export function useIntegrationHost(): IntegrationHost | null {
  return useContext(IntegrationContext);
}

/** The integrations a profile is bound to that the app found (through isBound). */
export function useBoundIntegrations(profileId: string): IntegrationInfo[] {
  const host = useIntegrationHost();
  // The hook is called whether or not there is a host.
  const list = useStore(host?.store ?? noStore, (state) => state.integrations);
  return useMemo(
    () => (host ? boundIntegrations(list, profileId) : []),
    [host, list, profileId],
  );
}

// A store that never has integrations, for useStore without a provider (the
// hooks run either way). Only integrations is ever read from it.
export const noStore = {
  getState: () => ({ integrations: null }),
  getInitialState: () => ({ integrations: null }),
  setState: () => {},
  subscribe: () => () => {},
} as unknown as PadmateStore;

/** The plain sentence for a failed integrations.* call. */
export function integrationFailure(
  error: unknown,
  appName: string,
  language: Language,
): string {
  const code = (error as { code?: unknown } | null)?.code;
  switch (code) {
    case INTEGRATION_RUNNING:
      return tx(
        language,
        `${appName} 还在运行，请先关闭。`,
        `${appName} is still running. Close it first.`,
      );
    case INTEGRATION_UNAVAILABLE:
      return tx(
        language,
        `现在读不到 ${appName} 的快捷键设置。`,
        `The shortcut settings of ${appName} cannot be read now.`,
      );
    case INTEGRATION_FILES_BUSY:
      return tx(
        language,
        `${appName} 的文件正在使用，或刚刚变了，什么都没有写入。请再试一次。`,
        `The files of ${appName} are in use or just changed. Nothing was written. Try again.`,
      );
    case INTEGRATION_REVISION:
      return tx(
        language,
        "这份配置的软件适配刚刚被改动，请再试一次。",
        "The integration of this profile just changed. Try again.",
      );
    default:
      return tx(language, "没有完成。", "It did not finish.");
  }
}

interface Waiting {
  info: IntegrationInfo;
  resolve: (closed: boolean) => void;
}

export function IntegrationProvider({
  store,
  language,
  children,
}: {
  store: PadmateStore;
  language: Language;
  children: ReactNode;
}) {
  const [waiting, setWaiting] = useState<Waiting | null>(null);
  const [still, setStill] = useState(false);
  const live = useRef(waiting);
  live.current = waiting;
  const languageRef = useRef(language);
  languageRef.current = language;

  const running = useCallback(
    async (info: IntegrationInfo): Promise<boolean> => {
      await store.getState().refreshIntegrations();
      const now = store
        .getState()
        .integrations?.find((entry) => entry.id === info.id);
      return (now ?? info).running;
    },
    [store],
  );

  const requireClosed = useCallback(
    async (info: IntegrationInfo): Promise<boolean> => {
      if (!(await running(info))) return true;
      return new Promise<boolean>((resolve) => {
        setStill(false);
        setWaiting({ info, resolve });
      });
    },
    [running],
  );

  const write = useCallback(
    async (
      info: IntegrationInfo,
      changes: IntegrationChange[],
    ): Promise<WriteOutcome> => {
      for (;;) {
        if (!(await requireClosed(info))) return { ok: false, cancelled: true };
        try {
          const written = await store
            .getState()
            .api.writeIntegration(info.id, changes);
          return { ok: true, written };
        } catch (error) {
          // It started between the check and the write: ask again.
          if ((error as { code?: unknown })?.code === INTEGRATION_RUNNING)
            continue;
          return {
            ok: false,
            cancelled: false,
            message: integrationFailure(error, info.name, languageRef.current),
          };
        }
      }
    },
    [requireClosed, store],
  );

  const host = useMemo<IntegrationHost>(
    () => ({ store, requireClosed, write }),
    [store, requireClosed, write],
  );

  // Never leave a caller waiting when the provider goes away.
  useEffect(
    () => () => {
      live.current?.resolve(false);
    },
    [],
  );

  const retry = async () => {
    const current = waiting;
    if (!current) return;
    if (await running(current.info)) {
      setStill(true);
      return;
    }
    setWaiting(null);
    current.resolve(true);
  };
  const cancel = () => {
    const current = waiting;
    setWaiting(null);
    current?.resolve(false);
  };
  const name = waiting?.info.name ?? "";
  return (
    <IntegrationContext.Provider value={host}>
      {children}
      <Dialog
        open={!!waiting}
        title={tx(language, `请先关闭 ${name}`, `Close ${name} first`)}
        closeLabel={tx(language, "关闭", "Close")}
        onClose={cancel}
        className="confirm-dialog"
        footer={
          <>
            <span className="spacer" />
            <button type="button" className="btn-ghost" onClick={cancel}>
              {tx(language, "取消", "Cancel")}
            </button>
            <button
              type="button"
              className="btn-primary"
              onClick={() => void retry()}
            >
              {tx(language, "重试", "Retry")}
            </button>
          </>
        }
      >
        <p className="dialog-text" data-testid="integration-running">
          {tx(
            language,
            `${name} 正在运行时不能改它的快捷键设置。关闭后点“重试”。`,
            `The shortcut settings of ${name} cannot be changed while it runs. Close it, then choose Retry.`,
          )}
        </p>
        {still && (
          <p className="dialog-text" role="alert">
            {tx(language, `${name} 还在运行。`, `${name} is still running.`)}
          </p>
        )}
      </Dialog>
    </IntegrationContext.Provider>
  );
}
