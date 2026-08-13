import React from "react";
import { createRoot } from "react-dom/client";
import "./app/index.css";
import { App } from "./app/App";
import { applyTheme, loadTheme } from "./app/theme";

applyTheme(loadTheme());

// The Wails runtime attaches window.wails (Call.ByName), which every native
// binding — engine supervision, the directory picker, OS notifications —
// checks for. It is an ES module, so it cannot ride a classic script tag
// (its `export` line kills the parse and nothing runs), and a static module
// tag would send vite resolving a path only the desktop's asset server
// serves. Imported dynamically: in a plain browser the request 404s, the
// catch swallows it, and the guards keep truthfully reporting absence.
// Through a variable so neither vite nor tsc tries to resolve a module that
// exists only behind the desktop's asset server.
const wailsRuntime = "/wails/runtime.js";
import(/* @vite-ignore */ wailsRuntime).catch(() => {});

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
