import { Badge, Button, Callout, RadioCards, TextField } from "@radix-ui/themes";
import { ArrowLeft, BookOpen, Check, Copy, ExternalLink, LoaderCircle, Rocket, TriangleAlert } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Answer, Cancel, DryRun, DryRunMode, Stages, Start } from "../wailsjs/go/main/App";
import { main } from "../wailsjs/go/models";
import { BrowserOpenURL, EventsOn } from "../wailsjs/runtime/runtime";
import "./App.css";
import Book, { BookHandoff } from "./Book";
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
  const [results, setResults] = useState<Record<string, main.StageResult>>({});
  const [started, setStarted] = useState(false);
  const [hasStarted, setHasStarted] = useState(false);
  const [returningToCover, setReturningToCover] = useState(false);
  const [running, setRunning] = useState(false);
  const [stopped, setStopped] = useState("");
  const [complete, setComplete] = useState(false);
  const [preflightStopped, setPreflightStopped] = useState("");
  const [live, setLive] = useState<Record<string, Status>>({});
  const [current, setCurrent] = useState<string | null>(null);
  const [notes, setNotes] = useState<Record<string, Line[]>>({});
  const [screen, setScreen] = useState<Screen | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [code, setCode] = useState<Code | null>(null);
  // A finished step the operator clicked to look back at; cleared as soon as setup moves on.
  const [looking, setLooking] = useState<string | null>(null);
  const [handoffScreen, setHandoffScreen] = useState<Screen | null>(null);
  const [handoffAnswer, setHandoffAnswer] = useState<string | null>(null);
  const [handoffClosed, setHandoffClosed] = useState(false);
  const checking = useRef(false);

  useEffect(() => {
    // Stage metadata is local. Checks are explicitly requested: merely opening
    // the window must not run preflight commands.
    if (!("runtime" in window)) return;
    DryRunMode().then(mode => { dryRunModeRef.current = mode; setDryRunMode(mode); });
    Stages().then(setStages).catch(e => setPreflightStopped(String(e)));
    const off = [
      EventsOn("screen", (s: Screen) => {
        setSubmitting(false);
        setLooking(null);
        setCurrent(s.stepId || null);
        setScreen(s);
      }),
      EventsOn("note", (n: Note) => {
        if (n.kind === "code") {
          setCode(n.message ? { code: n.message, link: n.link ?? "", stepId: n.stepId } : null);
          return;
        }
        // Any report after a typed answer means the engine accepted it.
        setSubmitting((was) => {
          if (was) setScreen(null);
          return false;
        });
        if (n.kind === "step-started" || n.kind === "step-finished" || n.kind === "step-failed") setCode(null);
        if (n.kind === "step-started") {
          setLooking(null);
          setCurrent(n.stepId);
          setLive((l) => ({ ...l, [n.stepId]: "running" }));
        } else if (n.kind === "step-finished") setLive((l) => ({ ...l, [n.stepId]: "done" }));
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
    setNotes({});
    setLive({});
    setStopped("");
    setComplete(false);
    setDryRunComplete(false);
    setHandoffScreen(null);
    setHandoffAnswer(null);
    setHandoffClosed(false);
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
    if (hasStarted) {
      setStarted(true);
      return true;
    }
    return start();
  }

  function back() {
    if (index <= 0) {
      setReturningToCover(true);
      return;
    }
    setLooking(stages[index - 1].id);
  }

  function answer(value: string) {
    // A typed answer is checked by the engine and may come back rejected, so the
    // question stays up and says so. A yes/no or a choice is final.
    if (screen?.stepId === "finalize") {
      setHandoffScreen(screen);
      setHandoffAnswer(value);
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
        <p className="chapter-purpose">{info?.purpose}</p>
        <span className="chapter-rule" />
        <div className="chapter-outcome"><small>{dryRunMode ? dryRunComplete ? "Rehearsal complete" : `${todo} part${todo === 1 ? "" : "s"} remaining` : complete ? "All quests complete" : `${todo} quest${todo === 1 ? "" : "s"} remaining`}</small><p>{info?.outcome}</p></div>
      </article>,
      <article className="action-page" aria-label={`Setup step ${pageIndex + 1}`} key={`${item.id}-action`}>
        <div className="eyebrow">Quest {pageIndex + 1} of {stages.length}</div>
        {code && item.id === code.stepId && item.id === shownId && <CodeCard code={code} simulation={dryRunMode} />}
        {stopped && item.id === shownId && <Callout.Root color="red" role="alert" mb="4"><Callout.Icon><TriangleAlert size={16} /></Callout.Icon><Callout.Text>Setup stopped: {stopped}</Callout.Text></Callout.Root>}
        {complete && !dryRunMode && item.id === shownId && <Callout.Root color="green" mb="4"><Callout.Icon><Check size={16} /></Callout.Icon><Callout.Text>All quests complete. Your server kit is ready.</Callout.Text></Callout.Root>}
        {dryRunComplete && item.id === shownId && <Callout.Root color="blue" mb="4"><Callout.Icon><Check size={16} /></Callout.Icon><Callout.Text>The UI rehearsal is complete. No services or files were changed.</Callout.Text></Callout.Root>}
        <div className="guide-card">
          {screen && screen.stepId === item.id ? <QuestionCard screen={screen} busy={submitting} simulation={dryRunMode} onAnswer={answer} onCancel={() => { setScreen(null); setSubmitting(false); Cancel(); }} /> : <StepView stage={{ ...item, title: journey[item.id]?.title ?? item.title }} status={statusOf(item.id)} result={results[item.id]} lines={notes[item.id] ?? []} />}
        </div>
      </article>,
    ];
  });

  if (!started) return <Welcome stages={stages} stopped={preflightStopped || stopped} resume={hasStarted} onStart={openFromCover} />;

  if (handoffClosed && handoffScreen) {
    const copyPath = !dryRunMode && handoffAnswer === "yes" ? handoffScreen.prompt.replace(/^Save a copy to\s*/, "").replace(/\?\s*$/, "") : undefined;
    return <BookHandoff isoPath={handoffScreen.intro ?? ""} copyPath={copyPath} instructions={(handoffScreen.steps ?? []).map(step => step.text)} simulation={dryRunMode} />;
  }

  return <Book
    pageIndex={Math.max(0, index)}
    chapter={Math.max(0, index)}
    title={stage ? journey[stage.id]?.title ?? stage.title : "Adaetum"}
    pages={stagePages}
    chapters={stages.map(s => ({ id: s.id, title: journey[s.id]?.title ?? s.title, status: statusOf(s.id), enabled: dryRunMode || statusOf(s.id) === "done" || s.id === shownId }))}
    onChapter={id => setLooking(id)}
    onPageChange={page => { const selected = stages[page]; if (selected) setLooking(selected.id); }}
    footer={<footer className="footer"><Button variant="soft" color="gray" onClick={back}><ArrowLeft size={15} /> Back</Button>{running && <Button variant="soft" color="gray" onClick={() => Cancel()}>Cancel</Button>}</footer>}
    closing={returningToCover || (complete && !!handoffScreen)}
    returnToCover={returningToCover}
    onClosed={() => {
      if (returningToCover) {
        setStarted(false);
        setReturningToCover(false);
      } else setHandoffClosed(true);
    }}
  />;
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
      <BookCover />
      <p className="closed-intro">Configure your installer ISO, install your private cloud on your servers, and connect an LLM to manage it.</p>
      <div className="actions welcome-actions">
        <Button size="3" onClick={() => setUnlockStage("shake")} disabled={!stages.length || unlockStage !== "idle"}>{resume ? <BookOpen size={16} /> : <Rocket size={16} />}{resume ? "Reopen your Adaetum" : "Create your Adaetum"}</Button>
      </div>
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

function CodeCard({ code, simulation = false }: { code: Code; simulation?: boolean }) {
  return (
    <div className="code-card" role="status">
      <div className="eyebrow">Connect your GitHub account</div>
      <p className="code-text">{simulation ? "Example device code for the UI rehearsal:" : "GitHub should have opened in your browser. Enter this code there:"}</p>
      <p className="code-text muted">{simulation ? "No browser sign-in or GitHub request will happen." : "Already have an account? Sign in and approve the code. New to GitHub? Create an account, then return here. Your account needs permission to create a private repository."}</p>
      <div className="code-row">
        <code className="code">{code.code}</code>
        <Button size="2" variant="soft" disabled={simulation} onClick={() => navigator.clipboard.writeText(code.code)}><Copy size={14} /> Copy</Button>
        <Button size="2" disabled={simulation} onClick={() => BrowserOpenURL(code.link)}><ExternalLink size={14} /> Open GitHub</Button>
        <Button size="2" variant="soft" disabled={simulation} onClick={() => BrowserOpenURL("https://github.com/signup")}><ExternalLink size={14} /> Create account</Button>
      </div>
      {!simulation && <p className="code-text muted">Waiting for you to approve it. This continues by itself.</p>}
    </div>
  );
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
function QuestionCard({ screen, busy, simulation = false, onAnswer, onCancel }: { screen: Screen; busy: boolean; simulation?: boolean; onAnswer: (v: string) => void; onCancel: () => void }) {
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

  return (
    <div className="qcard">
      <div className="action-label">YOUR OBJECTIVE</div>
      <h1 ref={heading} tabIndex={-1} className="title">{screen.title || (screen.kind === "choice" ? "Choose one" : lines[0])}</h1>
      {screen.intro && <p className="lede small">{screen.intro}</p>}

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
      {!screen.title && screen.kind === "choice" && screen.prompt && <p className="lede small">{screen.prompt}</p>}
      {screen.title && !confirm && screen.prompt && !guided && <p className="lede small">{screen.prompt}</p>}
      {screen.title && confirm && screen.prompt && <p className="lede small">{screen.prompt}</p>}

      {screen.error && !busy && (
        <Callout.Root color="red" role="alert" mt="4">
          <Callout.Icon><TriangleAlert size={16} /></Callout.Icon>
          <Callout.Text>{screen.error}</Callout.Text>
        </Callout.Root>
      )}

      {screen.kind === "choice" && (
        <RadioCards.Root value={text} onValueChange={setText} columns="1" mt="4">
          {screen.options.map((o) => (
            <RadioCards.Item key={o} value={o}>{o}</RadioCards.Item>
          ))}
        </RadioCards.Root>
      )}
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

      <div className="qactions">
        <Button size="3" loading={busy} onClick={() => onAnswer(confirm ? "yes" : text)}>{busy ? "Checking…" : yes}</Button>
        <Button size="3" variant="soft" color="gray" disabled={busy} onClick={confirm ? () => onAnswer("no") : onCancel}>{no}</Button>
      </div>
    </div>
  );
}
