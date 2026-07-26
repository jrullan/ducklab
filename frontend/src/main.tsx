import React from "react";
import { createRoot } from "react-dom/client";
import "./app/index.css";
import { App } from "./app/App";
import { applyTheme, loadTheme } from "./app/theme";

applyTheme(loadTheme());

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
