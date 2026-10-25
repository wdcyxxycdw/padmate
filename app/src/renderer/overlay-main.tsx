import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Overlay } from "./Overlay";
import "./overlay.css";

const root = document.getElementById("root");
if (!root) throw new Error("Missing overlay root");
createRoot(root).render(
  <StrictMode>
    <Overlay />
  </StrictMode>,
);
