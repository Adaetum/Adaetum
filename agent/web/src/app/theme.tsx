import { createContext, ReactNode, useContext, useEffect, useMemo, useState } from "react";
import { Theme } from "@radix-ui/themes";

export type ThemeMode = "system" | "light" | "dark";

const STORAGE_KEY = "adaetum.theme";

interface ThemeContextValue {
  mode: ThemeMode;
  setMode: (mode: ThemeMode) => void;
}

const ThemeContext = createContext<ThemeContextValue>({ mode: "system", setMode: () => {} });

export const useThemeMode = () => useContext(ThemeContext);

// Storage can throw (private windows, blocked site data); the UI must work
// without it, so every access is guarded.
function readStoredMode(): ThemeMode {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (stored === "light" || stored === "dark" || stored === "system") return stored;
  } catch {
    /* fall through to the default */
  }
  return "system";
}

function usePrefersDark(): boolean {
  const query = "(prefers-color-scheme: dark)";
  const [dark, setDark] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const media = window.matchMedia(query);
    const onChange = (event: MediaQueryListEvent) => setDark(event.matches);
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, []);
  return dark;
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [mode, setModeState] = useState<ThemeMode>(readStoredMode);
  const prefersDark = usePrefersDark();
  const appearance = mode === "system" ? (prefersDark ? "dark" : "light") : mode;

  const value = useMemo<ThemeContextValue>(
    () => ({
      mode,
      setMode: (next) => {
        setModeState(next);
        try {
          window.localStorage.setItem(STORAGE_KEY, next);
        } catch {
          /* the choice still applies for this session */
        }
      },
    }),
    [mode],
  );

  return (
    <ThemeContext.Provider value={value}>
      <Theme appearance={appearance} accentColor="indigo" grayColor="slate" radius="medium" panelBackground="solid">
        {children}
      </Theme>
    </ThemeContext.Provider>
  );
}
