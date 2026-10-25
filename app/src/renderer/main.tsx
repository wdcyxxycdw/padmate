import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { createPadmateStore } from "./store";

const root = document.getElementById("root");
if (!root) throw new Error("Missing renderer root");
createRoot(root).render(
  <StrictMode>
    <App store={createPadmateStore(window.padmate)} />
  </StrictMode>,
);
