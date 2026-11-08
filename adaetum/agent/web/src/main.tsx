import "@fontsource-variable/inter";
import "@fontsource-variable/jetbrains-mono";
import "@radix-ui/themes/styles.css";
import "./app/app.css";

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./app/App";
import { ThemeProvider } from "./app/theme";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <App />
    </ThemeProvider>
  </StrictMode>,
);
