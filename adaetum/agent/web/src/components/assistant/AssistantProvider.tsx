import { createContext, lazy, ReactNode, Suspense, useCallback, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { AssistantStatus, fetchAssistantStatus, useConversation } from "../../lib/assistantApi";
import { AuthRequiredError } from "../../lib/auth";
import { describeScreen } from "../../lib/pageContext";
import { getSharePage, rememberAssistantOpen, useAssistantMode, wasAssistantOpen } from "../../lib/preferences";

// One assistant for the whole page: a drawer on the right that any screen can open, with
// a question already in it. The conversation lives here, above the pages, so it survives
// moving between them and closing the drawer.

// The thread library and markdown renderer are a large part of the page's code and are
// only needed once someone opens the assistant, so they load then.
const AssistantDrawer = lazy(() => import("./AssistantDrawer"));

// Docking needs room: below this width the page and the panel cannot sit side by side.
const WIDE = "(min-width: 1201px)";
const subscribeWide = (listener: () => void) => {
  const query = window.matchMedia(WIDE);
  query.addEventListener("change", listener);
  return () => query.removeEventListener("change", listener);
};
const useIsWide = () => useSyncExternalStore(subscribeWide, () => window.matchMedia(WIDE).matches);

export interface AssistantApi {
  isOpen: boolean;
  // The panel is open beside the page rather than on top of it: the page has made room.
  docked: boolean;
  // What the page would send with the next message, or "" when the person has turned
  // sharing off.
  page: () => string;
  // Lets the screen on show describe itself. Pass null when it unmounts.
  registerPage: (owner: symbol, summary: string | null) => void;
  // Something is happening in the conversation (working, or waiting for an answer).
  busy: boolean;
  open: () => void;
  close: () => void;
  toggle: () => void;
  // Opens the drawer and starts a new conversation with this question. A button's press
  // is the person's own act, which is what sends it; a link alone never does. If the
  // current conversation is still going, it is left alone and only brought into view.
  ask: (question: string) => void;
}

const Context = createContext<AssistantApi | null>(null);

// useAssistantPage is how a screen tells the assistant exactly what it is showing, in a few
// plain lines, instead of leaving it to be read off the screen. Call it with null (or not
// at all) to fall back to the screen's text.
export function useAssistantPage(summary: string | null): void {
  const { registerPage } = useAssistant();
  const owner = useRef(Symbol("page")).current;
  useEffect(() => {
    registerPage(owner, summary);
    return () => registerPage(owner, null);
  }, [registerPage, owner, summary]);
}

export function useAssistant(): AssistantApi {
  const api = useContext(Context);
  if (!api) throw new Error("useAssistant must be used inside AssistantProvider");
  return api;
}

export function AssistantProvider({ children }: { children: ReactNode }) {
  // Open again after a reload if it was open before.
  const [isOpen, setOpen] = useState(wasAssistantOpen);
  const [everOpened, setEverOpened] = useState(wasAssistantOpen);
  const mode = useAssistantMode();
  const wide = useIsWide();
  const docked = isOpen && mode === "split" && wide;
  // The screen's own summary, if it gave one. Held in a ref: it changes as data arrives
  // and nothing needs to re-render for it, only the next message needs to read it.
  const registered = useRef<{ owner: symbol; summary: string } | null>(null);
  const registerPage = useCallback((owner: symbol, summary: string | null) => {
    if (summary !== null) registered.current = { owner, summary };
    else if (registered.current?.owner === owner) registered.current = null;
  }, []);
  const page = useCallback(() => (getSharePage() ? describeScreen(registered.current?.summary ?? null) : ""), []);
  const [status, setStatus] = useState<AssistantStatus | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);
  const conversation = useConversation(isOpen);
  const state = useRef(conversation.state);
  state.current = conversation.state;
  const startNew = useRef(conversation.startNew);
  startNew.current = conversation.startNew;
  const pageNow = useRef(page);
  pageNow.current = page;
  const known = useRef<AssistantStatus | null>(null);
  known.current = status;

  const open = useCallback(() => {
    setEverOpened(true);
    setOpen(true);
  }, []);
  const close = useCallback(() => setOpen(false), []);
  const toggle = useCallback(() => {
    setEverOpened(true);
    setOpen((current) => !current);
  }, []);
  useEffect(() => rememberAssistantOpen(isOpen), [isOpen]);

  // The status says whether the assistant is switched on; read it each time the drawer
  // opens, since it can be turned on or off on the AI model page meanwhile.
  useEffect(() => {
    if (!isOpen) return;
    const controller = new AbortController();
    fetchAssistantStatus(controller.signal)
      .then((next) => {
        setStatus(next);
        setStatusError(null);
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        // Not being signed in is shown by the drawer's own sign-in, from the conversation.
        if (!(err instanceof AuthRequiredError)) setStatusError(err instanceof Error ? err.message : String(err));
      });
    return () => controller.abort();
  }, [isOpen]);

  // Docking: the page makes room while the drawer is open beside it (see assistant.css).
  useEffect(() => {
    const root = document.documentElement;
    root.dataset.assistant = isOpen ? "open" : "closed";
    root.dataset.assistantDock = docked ? "true" : "false";
    return () => {
      delete root.dataset.assistant;
      delete root.dataset.assistantDock;
    };
  }, [isOpen, docked]);

  // Over the page there is no other way out of it than the close button, so Escape works too.
  useEffect(() => {
    if (!isOpen || docked) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [isOpen, docked]);

  const ask = useCallback(
    (question: string) => {
      open();
      if (state.current !== "idle") return;
      void (async () => {
        try {
          const current = known.current ?? (await fetchAssistantStatus());
          // Off: the drawer explains how to turn it on, and nothing is sent anywhere.
          if (current.enabled) await startNew.current(question, pageNow.current());
        } catch {
          /* the drawer shows why the assistant cannot be reached */
        }
      })();
    },
    [open],
  );

  const api = useMemo<AssistantApi>(
    () => ({ isOpen, docked, page, registerPage, busy: conversation.state !== "idle", open, close, toggle, ask }),
    [isOpen, docked, page, registerPage, conversation.state, open, close, toggle, ask],
  );

  return (
    <Context.Provider value={api}>
      {children}
      {everOpened && (
        <Suspense fallback={null}>
          <div hidden={!isOpen} className="assistant-slot">
            <AssistantDrawer conversation={conversation} status={status} statusError={statusError} page={page} onClose={close} />
          </div>
        </Suspense>
      )}
    </Context.Provider>
  );
}
