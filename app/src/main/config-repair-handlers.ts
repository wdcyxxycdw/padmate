import type { DaemonCall, EditorHandler } from "./library-handlers";
import { validateConfigBackup } from "./security";

/**
 * The dialog that tells of a repaired config.json (PRO-29, ADR-0057). The
 * renderer passes only the backup's file name (ConfigRepair.backup): 确定
 * tells the daemon the repair was shown, 打开文件夹 has Main select the
 * file in its data folder. show gets the checked name.
 */
export function configRepairHandlers(options: {
  call: DaemonCall;
  show: (backup: string) => void;
}): Record<string, EditorHandler> {
  return {
    configRepairSeen: {
      count: 1,
      run: async (backup): Promise<void> => {
        const name = validateConfigBackup(backup);
        const pending = options.call("config.repairSeen", { backup: name });
        if (!pending) throw new Error("Daemon is not connected");
        await pending;
      },
    },
    configBackupShow: {
      count: 1,
      run: async (backup): Promise<void> => {
        options.show(validateConfigBackup(backup));
      },
    },
  };
}
