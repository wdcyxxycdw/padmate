// What the key drawer shows of an integration (ARCHITECTURE §16.5), only for a
// profile that is bound to it: the button beside the shortcut field that opens
// the picker, and the line under the keys that names the function they are.
import { type ReactNode, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { useStore } from "zustand";
import type { IntegrationInfo } from "../../shared/integrations";
import {
  cycleNote,
  differenceRows,
  functionsById,
  itemFor,
  pathText,
} from "../state/integrations-model";
import type { Language } from "../state/store";
import { Icon } from "../ui/Icon";
import {
  type CaptureField,
  FunctionPicker,
  type Picked,
} from "./FunctionPicker";
import {
  noStore,
  useBoundIntegrations,
  useIntegrationHost,
} from "./IntegrationContext";
import "./integrations.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** What the drawer hands the shortcut field of a binding. */
export interface IntegrationLink {
  /** The library ID of the profile the drawer edits. */
  profile: string;
  Capture: CaptureField;
  /** Puts a picked function's keys into the binding (one edit, so one undo). */
  onPick: (picked: Picked, info: IntegrationInfo) => void;
  disabled?: boolean;
}

const stop = (event: { stopPropagation: () => void }) =>
  event.stopPropagation();

/**
 * A dialog of the integrations, drawn outside the field that opens it and cut
 * off from it. React sends the events of a portal on to the component that
 * rendered it, and a click inside the dialog would then start the field's
 * shortcut capture (the field around the button starts one on a click), which
 * records the next click as a mouse button: the mapping would become 左键
 * under the person's hands. Nothing of the dialog reaches the field.
 */
export function Isolated({ children }: { children: ReactNode }) {
  return createPortal(
    // biome-ignore lint/a11y/noStaticElementInteractions: it only stops the events of the dialog inside
    <div
      className="isolated"
      onClick={stop}
      onDoubleClick={stop}
      onMouseDown={stop}
      onMouseUp={stop}
      onContextMenu={stop}
      onKeyDown={stop}
      onKeyUp={stop}
      onFocus={stop}
      onBlur={stop}
    >
      {children}
    </div>,
    document.body,
  );
}

/** 从 <软件名> 选择… beside the shortcut field's own button. */
export function IntegrationPickButtons({
  link,
  keys,
  language,
}: {
  link: IntegrationLink;
  keys: string;
  language: Language;
}) {
  const bound = useBoundIntegrations(link.profile);
  const [open, setOpen] = useState<string | null>(null);
  return (
    <>
      {bound.map((info) => (
        <span key={info.id} className="drawer-capture-actions-item">
          <button
            type="button"
            className="btn drawer-capture-btn"
            disabled={link.disabled}
            data-testid={`pick-from-${info.id}`}
            // Not a mouse key of a capture that is running: opening the
            // dialog ends it (the focus leaves the field).
            data-no-capture=""
            onClick={(event) => {
              // The field around the button starts a capture on a click.
              event.stopPropagation();
              setOpen(info.id);
            }}
          >
            {tx(language, `从 ${info.name} 选择…`, `Pick from ${info.name}…`)}
          </button>
          <Isolated>
            <FunctionPicker
              open={open === info.id}
              info={info}
              profile={link.profile}
              currentKeys={keys}
              language={language}
              Capture={link.Capture}
              onClose={() => setOpen(null)}
              onPicked={(picked) => link.onPick(picked, info)}
            />
          </Isolated>
        </span>
      ))}
    </>
  );
}

/** "<软件> 功能：撤销 · 编辑", and what is different from the app now. */
export function IntegrationLine({
  profile,
  keys,
  language,
}: {
  profile: string;
  keys: string;
  language: Language;
}) {
  const bound = useBoundIntegrations(profile);
  return (
    <>
      {bound.map((info) => (
        <IntegrationLineOf
          key={info.id}
          info={info}
          profile={profile}
          keys={keys}
          language={language}
        />
      ))}
    </>
  );
}

function IntegrationLineOf({
  info,
  profile,
  keys,
  language,
}: {
  info: IntegrationInfo;
  profile: string;
  keys: string;
  language: Language;
}) {
  const host = useIntegrationHost();
  const store = host?.store ?? noStore;
  const dataKey = `${profile}|${info.id}`;
  // The hooks run whether or not there is a host (there is one here).
  const data = useStore(store, (state) => state.integrationItems[dataKey]);
  const functions = useStore(
    store,
    (state) => state.integrationFunctions[`${info.id}|${state.language}`],
  );
  useEffect(() => {
    if (!data && host)
      void store
        .getState()
        .loadIntegrationItems(profile, info.id)
        .catch(() => {});
  }, [data, host, store, profile, info.id]);
  const item = itemFor(data?.items, keys);
  useEffect(() => {
    if (item && !functions && host)
      void store
        .getState()
        .loadIntegrationFunctions(info.id)
        .catch(() => {});
  }, [item, functions, host, store, info.id]);
  if (!item || !data) return null;
  const fn = functionsById(functions?.functions ?? []).get(item.function);
  const cycle =
    fn && functions
      ? cycleNote(fn, functionsById(functions.functions), info.name, language)
      : null;
  const state = differenceRows(
    data.items,
    data.statuses.filter(
      (status) =>
        status.keys === item.keys && status.function === item.function,
    ),
    info.name,
    language,
  )[0];
  const path = item.path.length ? ` · ${pathText(item.path)}` : "";
  return (
    <div className="integration-line" data-testid="integration-line">
      <Icon name="info" size={14} stroke={2} />
      <span>
        {tx(
          language,
          `${info.name} 功能：${item.name}${path}`,
          `${info.name} function: ${item.name}${path}`,
        )}
        {state && (
          <span className="integration-line-warn" data-state={state.state}>
            {" "}
            {state.text}
          </span>
        )}
        {cycle && <span className="integration-line-note"> {cycle}</span>}
      </span>
    </div>
  );
}
