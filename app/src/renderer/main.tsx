import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { createPadmateStore } from "./state/store";

const root = document.getElementById("root");
if (!root) throw new Error("Missing renderer root");
const api = window.padmate;
// The language file first, so that the first paint is in its language
// (ADR-0018); a file that cannot be read is no file.
void Promise.resolve()
  .then(() => api.getLanguage())
  .catch(() => null)
  .then((savedLanguage) => {
    createRoot(root).render(
      <StrictMode>
        <App store={createPadmateStore(api, { savedLanguage })} />
      </StrictMode>,
    );
  });
