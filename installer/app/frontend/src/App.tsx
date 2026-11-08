import { Badge, Button, Callout, TextField } from "@radix-ui/themes";
import { ArrowLeft, Check, Copy, ExternalLink, LoaderCircle, TriangleAlert } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Answer, Cancel, DryRun, DryRunMode, Stages, Start } from "../wailsjs/go/main/App";
import { main } from "../wailsjs/go/models";
import { BrowserOpenURL, EventsOn } from "../wailsjs/runtime/runtime";
import "./App.css";
import Book, { BookHandoff, type BookPosition } from "./Book";
import BookCover from "./BookCover";
import CoverLock from "./CoverLock";
import { chapterNumerals, journey } from "./journey";

// Wails generates TypeScript only for types in bound method signatures; these travel
// in events, so they are declared here to match app.go.
interface Instruction { text: string; link?: string; command?: string }
export interface Screen {
  stepId: string; id: string; kind: string; prompt: string; options: string[]; default: string; error?: string;
  title?: string; intro?: string; steps?: Instruction[]; listTitle?: string; list?: string[]; yesLabel?: string; noLabel?: string;
}
interface Note { stepId: string; kind: string; message: string; link?: string }
interface Code { code: string; link: string; stepId: string }
interface Line { text: string; progress: boolean }
interface Selection { id: string; label: string; value: string }

// Only public configuration choices belong in the review. Credentials and
// arbitrary text answers must not be retained by the presentation layer.
const reviewLabels: Record<string, string> = {
  "repository-owner": "GitHub account or organization",
  "reuse-repository-select": "Recovery repository",
  repository: "Recovery repository",
  "cloudflare-zone": "Cloudflare zone",
  "tailscale-tailnet": "Tailscale tailnet",
  domain: "Public domain",
  "overlay-domain": "Tailscale domain",
  "media-type": "Installer image",
  "media-arch": "Target architecture",
};

type Status = "pending" | "needed" | "done" | "blocked" | "running" | "failed";

// Radix sizes everything from --scaling, and App.css multiplies its own sizes by the
// same number, so setting it scales the whole installer. The scale follows the tighter
// of width and height against the 1100x760 design size.
function useScale() {
  useEffect(() => {
    const apply = () => {
      const root = document.querySelector<HTMLElement>(".radix-themes");
      const fit = Math.min(window.innerWidth / 1100, window.innerHeight / 760);
      root?.style.setProperty("--scaling", Math.min(1.35, Math.max(0.8, fit)).toFixed(3));
    };
    apply();
    window.addEventListener("resize", apply);
    return () => window.removeEventListener("resize", apply);
  }, []);
}

// The window only draws. Order, checks, questions and what a step does are the Go
// engine's job; this page shows the step that is happening, asks what it asks, and
// sends the answer back.
export default function App() {
  useScale();
  const [dryRunMode, setDryRunMode] = useState(false);
  const dryRunModeRef = useRef(false);
  const [dryRunComplete, setDryRunComplete] = useState(false);
  const [stages, setStages] = useState<main.Stage[]>([]);
  const [githubUser, setGithubUser] = useState("");
  const [results, setResults] = useState<Record<string, main.StageResult>>({});
  const [started, setStarted] = useState(false);
  const [hasStarted, setHasStarted] = useState(false);
  const [coverResume, setCoverResume] = useState(false);
  const [returningToCover, setReturningToCover] = useState(false);
  const [running, setRunning] = useState(false);
  const [stopped, setStopped] = useState("");
  const [toast, setToast] = useState<{ message: string } | null>(null);
  const [complete, setComplete] = useState(false);
  const [preflightStopped, setPreflightStopped] = useState("");
  const [live, setLive] = useState<Record<string, Status>>({});
  const [current, setCurrent] = useState<string | null>(null);
  const [notes, setNotes] = useState<Record<string, Line[]>>({});
  const [selections, setSelections] = useState<Record<string, Selection[]>>({});
  const pendingSelection = useRef<{ stepId: string; selection: Selection } | null>(null);
  const [screen, setScreen] = useState<Screen | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [code, setCode] = useState<Code | null>(null);
  // A finished step the operator clicked to look back at; cleared as soon as setup moves on.
  const [looking, setLooking] = useState<string | null>(null);
  const [handoffScreen, setHandoffScreen] = useState<Screen | null>(null);
  const [handoffAnswer, setHandoffAnswer] = useState<string | null>(null);
  const [handoffClosed, setHandoffClosed] = useState(false);
  const [handoffPosition, setHandoffPosition] = useState<BookPosition | undefined>();
  const [cancelPending, setCancelPending] = useState(false);
  const cancelReset = useRef(false);
  const checking = useRef(false);

  function savePendingSelection() {
    const pending = pendingSelection.current;
    pendingSelection.current = null;
    if (!pending) return;
    setSelections(all => ({ ...all, [pending.stepId]: [...(all[pending.stepId] ?? []).filter(s => s.label !== pending.selection.label), pending.selection] }));
  }

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(null), 7000);
    return () => window.clearTimeout(timer);
  }, [toast]);

  useEffect(() => {
    // Stage metadata is local. Checks are explicitly requested: merely opening
    // the window must not run preflight commands.
    if (!("runtime" in window)) return;
    DryRunMode().then(mode => { dryRunModeRef.current = mode; setDryRunMode(mode); });
    Stages().then(setStages).catch(e => setPreflightStopped(String(e)));
    const off = [
      EventsOn("screen", (s: Screen) => {
        if (cancelReset.current) return;
        if (pendingSelection.current && (s.id !== pendingSelection.current.selection.id || s.stepId !== pendingSelection.current.stepId)) savePendingSelection();
        else if (s.error) pendingSelection.current = null;
        if (s.error) setToast({ message: s.error });
        setSubmitting(false);
        setLooking(null);
        setCurrent(s.stepId || null);
        setScreen({ ...s, error: undefined });
      }),
      EventsOn("note", (n: Note) => {
        if (cancelReset.current) return;
        if (n.kind === "github-account") { setGithubUser(n.message); return; }
        if (n.kind === "step-reset" || n.kind === "step-retry") {
          pendingSelection.current = null;
          setLooking(null);
          setCurrent(n.stepId);
          setSubmitting(false);
          setScreen(null);
          setCode(null);
          setHandoffScreen(null);
          setHandoffAnswer(null);
          setLive(l => ({ ...l, [n.stepId]: n.kind === "step-reset" ? "running" : "needed" }));
          setNotes(all => ({ ...all, [n.stepId]: [] }));
          if (n.kind === "step-retry" && n.message) setToast({ message: n.message });
          return;
        }
        if (n.kind === "code") {
          setCode(n.message ? { code: n.message, link: n.link ?? "", stepId: n.stepId } : null);
          return;
        }
        // A progress event can arrive immediately before the next question.
        // Do not clear the screen from a state updater: React may run it after
        // that question is displayed, leaving the engine waiting on a hidden form.
        if (n.kind === "step-started" || n.kind === "step-finished" || n.kind === "step-failed") setCode(null);
        if (n.kind === "step-started") {
          setLooking(null);
          setCurrent(n.stepId);
          setLive((l) => ({ ...l, [n.stepId]: "running" }));
        } else if (n.kind === "step-finished") { savePendingSelection(); setLive((l) => ({ ...l, [n.stepId]: "done" })); }
        else if (n.kind === "step-failed") setLive((l) => ({ ...l, [n.stepId]: "failed" }));
        if (n.message)
          setNotes((all) => {
            const list = all[n.stepId] ?? [];
            const progress = n.kind === "progress";
            // A progress line replaces the previous progress line instead of adding a row.
            const keep = progress && list.length > 0 && list[list.length - 1].progress ? list.slice(0, -1) : list;
            return { ...all, [n.stepId]: [...keep, { text: n.message, progress }] };
          });
      }),
      EventsOn("finished", (r: main.Report) => {
        if (cancelReset.current) {
          setRunning(false);
          return;
        }
        setRunning(false);
        setScreen(null);
        setCode(null);
        setStopped(r.stopped ?? "");
        if (dryRunModeRef.current) {
          setLive({});
          setResults(Object.fromEntries((r.results ?? []).map(result => [result.id, result])));
          setDryRunComplete(!r.stopped);
          setComplete(!r.stopped);
          return;
        }
        setComplete(!r.stopped);
        check();
      }),
    ];
    return () => off.forEach((f) => f());
  }, []);

  // Refresh stage status after a real run completes.
  async function check() {
    if (checking.current) return;
    checking.current = true;
    try {
      const report = await DryRun();
      setResults(Object.fromEntries((report.results ?? []).map((r) => [r.id, r])));
      setPreflightStopped(report.stopped ?? "");
    } catch (e) { setPreflightStopped(String(e)); }
    finally { checking.current = false; }
  }

  async function start(): Promise<boolean> {
    cancelReset.current = false;
    setToast(null);
    setNotes({});
    setSelections({});
    pendingSelection.current = null;
    setLive({});
    setStopped("");
    setComplete(false);
    setDryRunComplete(false);
    setCancelPending(false);
    setHandoffScreen(null);
    setHandoffAnswer(null);
    setHandoffClosed(false);
    setHandoffPosition(undefined);
    const problem = await Start();
    if (problem) {
      setStopped(problem);
      return false;
    }
    else {
      setStarted(true);
      setHasStarted(true);
      setRunning(true);
      return true;
    }
  }

  async function openFromCover(): Promise<boolean> {
    setCoverResume(hasStarted);
    if (hasStarted) {
      setStarted(true);
      return true;
    }
    return start();
  }

  function back() {
    setCancelPending(false);
    if (index <= 0) {
      setReturningToCover(true);
      return;
    }
    setLooking(stages[index - 1].id);
  }

  function requestCancel() {
    setCancelPending(true);
  }

  function cancelSetup() {
    if (!cancelPending) {
      setCancelPending(true);
      return;
    }
    cancelReset.current = true;
    Cancel();
    setReturningToCover(true);
    setCancelPending(false);
  }

  // Keep the visible sheet intact through the same close animation as Back.
  // Late engine events are ignored until a fresh run starts.
  function clearCancelledRun() {
    setToast(null);
    setStopped("");
    setPreflightStopped("");
    setScreen(null);
    setSubmitting(false);
    setCode(null);
    setLooking(null);
    setCurrent(null);
    setLive({});
    setResults({});
    setNotes({});
    setSelections({});
    pendingSelection.current = null;
    setComplete(false);
    setDryRunComplete(false);
    setHandoffScreen(null);
    setHandoffAnswer(null);
    setHandoffClosed(false);
    setHandoffPosition(undefined);
    setHasStarted(false);
  }

  function answer(value: string) {
    // A typed answer is checked by the engine and may come back rejected, so the
    // question stays up and says so. A yes/no or a choice is final.
    if (screen?.stepId === "finalize") {
      setHandoffScreen(screen);
      setHandoffAnswer(value);
    }
    if (screen && screen.kind !== "secret" && reviewLabels[screen.id] && value.trim()) {
      const selection = { id: screen.id, label: reviewLabels[screen.id], value: value.trim() };
      pendingSelection.current = { stepId: screen.stepId, selection };
    }
    if (screen && (screen.kind === "secret" || screen.kind === "text")) setSubmitting(true);
    else setScreen(null);
    Answer(value);
  }

  const statusOf = (id: string): Status => live[id] ?? (results[id]?.status as Status) ?? "pending";
  // The step on screen: one being looked back at, else the one asking or running, else the
  // first that is not done.
  const shownId = looking ?? current ?? stages.find((s) => statusOf(s.id) !== "done")?.id ?? stages[0]?.id;
  const index = stages.findIndex((s) => s.id === shownId);
  const stage = index >= 0 ? stages[index] : undefined;
  const todo = stages.filter((s) => statusOf(s.id) !== "done").length;
  const stagePages = stages.flatMap((item, pageIndex) => {
    const info = journey[item.id];
    return [
      <article className="chapter-page" key={`${item.id}-chapter`}>
        <div className="eyebrow">Quest {chapterNumerals[pageIndex] ?? pageIndex + 1}</div>
        <h1 className="chapter-title">{info?.title ?? item.title}</h1>
        <span className="chapter-rule" />
        <div className="chapter-overview" aria-label="Step overview">
          <div className="chapter-overview-block">
            <small>WHAT HAPPENS</small>
            <p>{info?.purpose}</p>
          </div>
          <div className="chapter-overview-block">
            <small>YOU’LL LEAVE WITH</small>
            <p>{info?.outcome}</p>
          </div>
        </div>
        <div className="chapter-outcome"><small>{dryRunMode ? dryRunComplete ? "Rehearsal complete" : `${todo} part${todo === 1 ? "" : "s"} remaining` : complete ? "All quests complete" : `${todo} quest${todo === 1 ? "" : "s"} remaining`}</small></div>
      </article>,
      <article className="action-page" aria-label={`Setup step ${pageIndex + 1}`} key={`${item.id}-action`}>
        <div className="eyebrow">Quest {pageIndex + 1} of {stages.length}</div>
        {code && item.id === code.stepId && item.id === shownId && !screen && <CodeCard code={code} simulation={dryRunMode} />}
        {stopped && item.id === shownId && <Callout.Root color="red" role="alert" mb="4"><Callout.Icon><TriangleAlert size={16} /></Callout.Icon><Callout.Text>Setup stopped: {stopped}</Callout.Text></Callout.Root>}
        {complete && !dryRunMode && item.id === shownId && <Callout.Root color="green" mb="4"><Callout.Icon><Check size={16} /></Callout.Icon><Callout.Text>All quests complete. Your server kit is ready.</Callout.Text></Callout.Root>}
        {dryRunComplete && item.id === shownId && <Callout.Root color="blue" mb="4"><Callout.Icon><Check size={16} /></Callout.Icon><Callout.Text>The UI rehearsal is complete. No services or files were changed.</Callout.Text></Callout.Root>}
        <div className="guide-card">
          {screen && screen.stepId === item.id ? <QuestionCard screen={screen} busy={submitting} simulation={dryRunMode} code={code && code.stepId === item.id && item.id === shownId ? code : undefined} githubUser={githubUser} onAnswer={answer} /> : looking === item.id && (selections[item.id]?.length ?? 0) > 0 ? <SelectionReview selections={selections[item.id]} /> : <StepView stage={{ ...item, title: journey[item.id]?.title ?? item.title }} status={statusOf(item.id)} result={results[item.id]} lines={notes[item.id] ?? []} />}
        </div>
      </article>,
    ];
  });

  if (!started) return <Welcome stages={cancelReset.current && running ? [] : stages} stopped={preflightStopped || stopped} resume={hasStarted} onStart={openFromCover} />;

  const finishing = complete && !!handoffScreen;
  const showHandoff = (finishing || handoffClosed) && !returningToCover && !!handoffScreen;
  const copyPath = !dryRunMode && handoffAnswer === "yes" ? handoffScreen?.prompt.replace(/^Save a copy to\s*/, "").replace(/\?\s*$/, "") : undefined;

  return <>
    {showHandoff && <BookHandoff isoPath={handoffScreen?.intro ?? "example/adaetum-installer.iso"} copyPath={copyPath} instructions={(handoffScreen?.steps ?? []).map(step => step.text)} simulation={dryRunMode} from={handoffPosition} closing={!handoffClosed} />}
    {toast && <div className="setup-toast" role="status" aria-live="polite"><span>{toast.message}</span><button type="button" aria-label="Dismiss notification" onClick={() => setToast(null)}>×</button></div>}
    {!handoffClosed && <div className={showHandoff ? "book-over-handoff" : undefined}><Book
    activity={running ? (code || (screen && !submitting) ? "waiting" : "working") : undefined}
    coverResume={returningToCover ? !cancelReset.current : coverResume}
    pageIndex={Math.max(0, index)}
    chapter={Math.max(0, index)}
    title={stage ? journey[stage.id]?.title ?? stage.title : "Adaetum"}
    pages={stagePages}
    chapters={stages.map(s => ({ id: s.id, title: journey[s.id]?.title ?? s.title, status: statusOf(s.id), enabled: dryRunMode || statusOf(s.id) === "done" || s.id === shownId }))}
    onChapter={id => setLooking(id)}
    onPageChange={page => { const selected = stages[page]; if (selected) setLooking(selected.id); }}
    footer={<footer className="footer"><Button variant="soft" color="gray" onClick={back}><ArrowLeft size={15} /> Back</Button>{running && <Button variant={cancelPending ? "solid" : "soft"} color={cancelPending ? "red" : "gray"} onClick={cancelPending ? cancelSetup : requestCancel}>{cancelPending ? "Confirm cancel" : "Cancel"}</Button>}</footer>}
    closing={returningToCover || finishing}
    returnToCover={returningToCover}
    onClosed={(position) => {
      if (returningToCover) {
        setStarted(false);
        if (cancelReset.current) clearCancelledRun();
        setReturningToCover(false);
      } else { setHandoffPosition(position); setHandoffClosed(true); }
    }}
  /></div>}</>;
}

// The first screen introduces the setup journey before opening the book.
function Welcome({ stages, stopped, resume, onStart }: {
  stages: main.Stage[];
  stopped: string;
  resume: boolean;
  onStart: () => Promise<boolean>;
}) {
  const [unlockStage, setUnlockStage] = useState<"idle" | "shake" | "unlocking">("idle");
  const startedFromCover = useRef(false);

  function releaseLock() {
    if (startedFromCover.current) return;
    startedFromCover.current = true;
    void onStart().then((started) => {
      if (!started) {
        startedFromCover.current = false;
        setUnlockStage("idle");
      }
    });
  }

  return (
    <div className="closed-welcome book-desk" data-resume={resume}>
      <div className="drag" />
      <div className="book-toolbar"><span>ADAETUM <i> / </i> FIRST SERVER SETUP</span></div>
      <div className="closed-stage">
      <div
        className="closed-grimoire"
        data-unlock-stage={unlockStage}
        onAnimationEnd={(event) => {
          if (event.target === event.currentTarget && unlockStage === "shake") setUnlockStage("unlocking");
        }}
      >
      <div className="closed-cover-content">
      <BookCover resume={resume} onStart={() => { if (unlockStage === "idle") setUnlockStage("shake"); }} disabled={!stages.length} />
      {stopped && (
        <Callout.Root color="red" role="alert" mt="4">
          <Callout.Icon><TriangleAlert size={16} /></Callout.Icon>
          <Callout.Text>{resume ? "Setup stopped: " : "Adaetum could not begin: "}{stopped}</Callout.Text>
        </Callout.Root>
      )}
      </div>
      <CoverLock phase={unlockStage} onAnimationEnd={() => {
        if (unlockStage === "unlocking") releaseLock();
      }} />
      </div>
      </div>
    </div>
  );
}

function CodeCard({ code, simulation = false, embedded = false }: { code: Code; simulation?: boolean; embedded?: boolean }) {
  return (
    <div className={`code-card${embedded ? " code-card-embedded" : ""}`} role="status">
      {!embedded && <div className="eyebrow">Connect your GitHub account</div>}
      {!embedded && <p className="code-text">{simulation ? "Example device code for the UI rehearsal:" : "GitHub should have opened in your browser. Enter this code there:"}</p>}
      {!embedded && <p className="code-text muted">{simulation ? "No browser sign-in or GitHub request will happen." : "Already have an account? Sign in and approve the code. New to GitHub? Create an account, then return here. Your account needs permission to create a private repository."}</p>}
      <div className="code-row">
        <code className="code">{code.code}</code>
        <Button size="2" variant="soft" disabled={simulation} onClick={() => navigator.clipboard.writeText(code.code)}><Copy size={14} /> Copy</Button>
        <Button size="2" disabled={simulation} onClick={() => BrowserOpenURL(code.link)}><ExternalLink size={14} /> Open GitHub</Button>
      </div>
      {!simulation && <p className="code-text muted">Waiting for you to approve it. This continues by itself.</p>}
    </div>
  );
}

function SelectionReview({ selections }: { selections: Selection[] }) {
  return <section className="selection-review" aria-label="Your selections">
    <h1 className="title">Your selections</h1>
    <dl>{selections.map(selection => <div key={selection.id}><dt>{selection.label}</dt><dd>{selection.value}</dd></div>)}</dl>
    <p className="lede small">Editing completed selections is not available in this run.</p>
  </section>;
}

// What a step looks like when it is not asking anything: a live checklist while it runs,
// the result when it is done, and the reason when something is in the way.
function StepView({ stage, status, result, lines }: { stage: main.Stage; status: Status; result?: main.StageResult; lines: Line[] }) {
  const running = status === "running";
  return (
    <>
      <h1 className="title">{stage.title}</h1>
      {status === "blocked" && result?.detail && (
        <Callout.Root color="red" role="alert" mt="4">
          <Callout.Icon><TriangleAlert size={16} /></Callout.Icon>
          <Callout.Text>{result.detail}</Callout.Text>
        </Callout.Root>
      )}
      {status === "failed" && <Badge color="red" size="2" mt="3">This step failed</Badge>}
      {lines.length > 0 ? (
        <ul className="activity">
          {lines.map((l, i) => {
            const last = i === lines.length - 1;
            return (
              <li key={i} data-active={running && last ? "" : undefined}>
                {running && last ? <LoaderCircle size={16} className="spin" /> : <Check size={16} />}
                <span>{l.text}</span>
              </li>
            );
          })}
        </ul>
      ) : running ? (
        <p className="quiet"><LoaderCircle size={16} className="spin" /> Quest in progress…</p>
      ) : status === "done" ? (
        <p className="quiet"><Check size={16} /> Quest complete.</p>
      ) : (
        <p className="quiet">{result?.plan?.[0] ?? "Waiting for the previous quest."}</p>
      )}
      {!running && status !== "done" && (result?.plan?.length ?? 0) > 1 && (
        <details className="details">
          <summary>Quest briefing</summary>
          <ul className="plain">{result!.plan.slice(0, -1).map((p) => <li key={p}>{p}</li>)}</ul>
        </details>
      )}
    </>
  );
}

// One question, in the step it belongs to. A question that needs the operator to do
// something first shows numbered actions and a short list; a plain question is just
// the question and its control.
function QuestionCard({ screen, busy, simulation = false, code, githubUser, onAnswer }: { screen: Screen; busy: boolean; simulation?: boolean; code?: Code; githubUser?: string; onAnswer: (v: string) => void }) {
  const [text, setText] = useState("");
  useEffect(() => setText(screen.default ?? ""), [screen]);
  const confirm = screen.kind === "confirm";
  const typed = screen.kind === "secret" || screen.kind === "text";
  const heading = useRef<HTMLHeadingElement>(null);
  // A new page needs a useful keyboard destination. Token/text fields already
  // autofocus; other questions announce their heading without scrolling the book.
  useEffect(() => {
    if (!typed) heading.current?.focus({ preventScroll: true });
  }, [screen.id, typed]);
  const guided = (screen.steps?.length ?? 0) > 0;
  const lines = screen.prompt.split("\n").filter((l) => l.trim() !== "");
  const isLink = (l: string) => /^https:\/\/\S+$/.test(l.trim());
  const isCommand = (l: string) => /^(gh|git|task) /.test(l);
  const yes = screen.yesLabel || (confirm ? "Continue" : screen.kind === "secret" ? "Check token" : "Continue");
  const no = screen.noLabel || (confirm ? "No" : "Cancel");
  const autoAdvance = simulation && screen.id === "github-signin";
  // A discovered value is still a selection when there is only one option.
  // Single-action screens with an explicit label (such as Retry) keep that label.
  const multiChoice = screen.kind === "choice" && screen.options.length > 0 && (screen.options.length > 1 || !screen.yesLabel);

  useEffect(() => {
    if (!autoAdvance) return;
    const timer = window.setTimeout(() => onAnswer("yes"), 1800);
    return () => window.clearTimeout(timer);
  }, [autoAdvance, onAnswer]);

  return (
    <div className="qcard" data-screen={screen.id}>
      {code && <div className="action-label">CONNECT YOUR GITHUB ACCOUNT</div>}
      {!code && <h1 ref={heading} tabIndex={-1} className="title">{screen.title || (screen.kind === "choice" ? screen.prompt : lines[0])}</h1>}
      {screen.id === "create-or-reuse-repository" && githubUser && <div className="github-account"><span>Signed in to GitHub as <strong>{githubUser}</strong></span><Button size="2" variant="soft" color="gray" onClick={() => onAnswer("__switch_github_account__")}>Log out</Button></div>}
      {screen.intro && !code && <p className="lede small">{screen.intro}</p>}
      {code && <CodeCard code={code} simulation={simulation} embedded />}

      {guided && (
        <ol className="instr">
          {screen.steps!.map((s, i) => (
            <li key={i}>
              <span className="num">{i + 1}</span>
              <span className="instr-text">{s.text}</span>
              {s.link && <Button size="1" variant="soft" disabled={simulation} onClick={() => BrowserOpenURL(s.link!)}><ExternalLink size={13} /> Open</Button>}
              {s.command && <Button size="1" variant="soft" disabled={simulation} onClick={() => navigator.clipboard.writeText(s.command!)}><Copy size={13} /> Copy</Button>}
            </li>
          ))}
        </ol>
      )}
      {(screen.list?.length ?? 0) > 0 && (
        <div className="chips-block">
          {screen.listTitle && <div className="chips-title">{screen.listTitle}</div>}
          <div className="chips">{screen.list!.map((c) => <span className="chip" key={c}>{c}</span>)}</div>
        </div>
      )}

      {/* A question with no structured fields still gets its text, with links and commands as boxes. */}
      {!guided && !screen.title && screen.kind !== "choice" && lines.slice(1).map((line, i) =>
        isLink(line) || isCommand(line) ? (
          <div className="command" key={i}>
            <code>{line.trim()}</code>
            {isLink(line) ? (
              <Button size="1" variant="ghost" disabled={simulation} onClick={() => BrowserOpenURL(line.trim())} aria-label="Open in browser"><ExternalLink size={13} /></Button>
            ) : (
              <Button size="1" variant="ghost" disabled={simulation} onClick={() => navigator.clipboard.writeText(line)} aria-label="Copy command"><Copy size={13} /></Button>
            )}
          </div>
        ) : (
          <p className="lede small" key={i}>{line}</p>
        ),
      )}
      {screen.title && !confirm && screen.prompt && !guided && <p className="lede small">{screen.prompt}</p>}
      {screen.title && confirm && screen.prompt && <p className="lede small">{screen.prompt}</p>}

      {screen.error && !busy && (
        <Callout.Root color="red" role="alert" mt="4">
          <Callout.Icon><TriangleAlert size={16} /></Callout.Icon>
          <Callout.Text>{screen.error}</Callout.Text>
        </Callout.Root>
      )}

      {multiChoice && <div className="choice-actions">
        {screen.options.map((o) => <Button key={o} size="3" onClick={() => onAnswer(o)}>{o}</Button>)}
      </div>}
      {typed && (
        <div className="field">
          <label htmlFor={`answer-${screen.id}`}>{screen.prompt}</label>
          <TextField.Root
            id={`answer-${screen.id}`}
            size="3"
            autoFocus
            disabled={busy}
            type={screen.kind === "secret" ? "password" : "text"}
            placeholder={screen.kind === "secret" ? screen.prompt : undefined}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && !busy && onAnswer(text)}
          />
        </div>
      )}

      {!autoAdvance && !multiChoice && <div className="qactions">
        <Button size="3" loading={busy} onClick={() => onAnswer(confirm ? "yes" : text)}>{busy ? "Checking…" : yes}</Button>
        {confirm && no.toLowerCase() !== "cancel" && <Button size="3" variant="soft" color="gray" disabled={busy} onClick={() => onAnswer("no")}>{no}</Button>}
      </div>}
    </div>
  );
}
