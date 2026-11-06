import { forwardRef, useEffect, useRef, useState, type ReactNode } from "react";
import HTMLFlipBook from "react-pageflip";
import { Button, Theme } from "@radix-ui/themes";
import { Check, Copy, Volume2, VolumeX } from "lucide-react";
import { chapterNumerals } from "./journey";
import "./Book.css";
import BookCover from "./BookCover";

export type ChapterTab = { id: string; title: string; status: string; enabled?: boolean };

export function BookHandoff({ isoPath, copyPath, instructions }: { isoPath: string; copyPath?: string; instructions: string[] }) {
  const [copied, setCopied] = useState(false);
  async function copyIsoPath() {
    try {
      await navigator.clipboard.writeText(copyPath || isoPath);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      setCopied(false);
    }
  }

  return <Theme appearance="light" accentColor="gold" grayColor="sand" radius="small" className="grimoire-theme">
    <main className="handoff-screen">
      <div className="book-toolbar"><span>ADAETUM <i> / </i> YOUR FIRST SERVER</span></div>
      <div className="handoff-stage">
        <div className="handoff-closed-book" aria-label="The Adaetum grimoire, closed">
          <div className="handoff-book-pages" aria-hidden="true" />
          <div className="handoff-book-cover"><BookCover /></div>
        </div>
        <aside className="handoff-note" aria-labelledby="handoff-title">
          <div className="handoff-note-heading"><span>FIELD NOTE · FINAL LEAF</span><span>VIII</span></div>
          <h1 id="handoff-title">Your installer is ready</h1>
          <p className="handoff-note-intro">The setup is complete. Keep this note with your installer ISO.</p>
          <div className="handoff-path"><span>{copyPath ? "ISO · DOWNLOADS COPY" : "INSTALLER ISO"}</span><code>{copyPath || isoPath}</code></div>
          {copyPath && <div className="handoff-path secondary"><span>ORIGINAL ISO</span><code>{isoPath}</code></div>}
          <h2>When you’re ready</h2>
          <ol className="handoff-instructions">{instructions.map((instruction, i) => <li key={i}><span>{i + 1}</span><p>{instruction}</p></li>)}</ol>
          <Button size="2" variant="soft" onClick={copyIsoPath}>{copied ? <Check size={14} /> : <Copy size={14} />}{copied ? "Copied path" : "Copy ISO path"}</Button>
        </aside>
      </div>
    </main>
  </Theme>;
}

type FlipApi = { getCurrentPageIndex: () => number; getSettings: () => { flippingTime: number }; flipNext: () => void; flipPrev: () => void; turnToPage: (page: number) => void };
type FlipHandle = { pageFlip: () => FlipApi };
type BookLeafProps = { children: ReactNode; className?: string; hard?: boolean };
const BookLeaf = forwardRef<HTMLDivElement, BookLeafProps>(function BookLeaf({ children, className = "", hard = false }, ref) {
  return <div ref={ref} className={`grimoire-page ${className}`} data-density={hard ? "hard" : "soft"}>{children}</div>;
});

let pageAudio: AudioContext | null = null;

// Keep the sound local and optional so the installer has no remote media dependency.
function playPageTurn() {
  try {
    pageAudio ??= new AudioContext();
    const context = pageAudio;
    if (context.state === "suspended") void context.resume();
    const length = Math.floor(context.sampleRate * 0.42);
    const buffer = context.createBuffer(1, length, context.sampleRate);
    const samples = buffer.getChannelData(0);
    for (let i = 0; i < length; i++) samples[i] = (Math.random() * 2 - 1) * (1 - i / length);
    const source = context.createBufferSource();
    const filter = context.createBiquadFilter();
    const gain = context.createGain();
    source.buffer = buffer;
    filter.type = "bandpass";
    filter.frequency.setValueAtTime(850, context.currentTime);
    filter.frequency.exponentialRampToValueAtTime(2600, context.currentTime + 0.16);
    filter.frequency.exponentialRampToValueAtTime(700, context.currentTime + 0.4);
    filter.Q.value = 0.55;
    gain.gain.setValueAtTime(0.0001, context.currentTime);
    gain.gain.exponentialRampToValueAtTime(0.045, context.currentTime + 0.035);
    gain.gain.exponentialRampToValueAtTime(0.0001, context.currentTime + 0.42);
    source.connect(filter).connect(gain).connect(context.destination);
    source.start();
    source.stop(context.currentTime + 0.42);
  } catch {
    // Sound is decorative; unavailable audio must not interrupt setup.
  }
}

// PageFlip owns only physical page movement. Adaetum's engine and React pages
// continue to own all setup state, questions and provider interactions.
export default function Book({ pageIndex, chapter, title, className = "", chapters = [], onChapter, onPageChange, footer, closing = false, onClosed, dryRun = false, pages }: {
  pageIndex: number;
  chapter: number;
  title: string;
  className?: string;
  chapters?: ChapterTab[];
  onChapter?: (id: string) => void;
  onPageChange?: (pageIndex: number) => void;
  footer?: ReactNode;
  closing?: boolean;
  onClosed?: () => void;
  dryRun?: boolean;
  pages: ReactNode[];
}) {
  const flipRef = useRef<FlipHandle | null>(null);
  const [sound, setSound] = useState(true);
  const [motion, setMotion] = useState(true);
  const [reduced, setReduced] = useState(() => window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  const [opening, setOpening] = useState(true);
  const ready = useRef(false);
  const closed = useRef(false);
  const openingTurnStarted = useRef(false);
  const targetLeaf = useRef<number | null>(null);
  const openFallback = useRef<number | null>(null);

  function turnToward(target: number) {
    const flip = flipRef.current?.pageFlip();
    if (!flip) return;
    const current = flip.getCurrentPageIndex();
    if (current === target) return;
    if (current < target) flip.flipNext();
    else flip.flipPrev();
  }

  useEffect(() => {
    const preference = window.matchMedia("(prefers-reduced-motion: reduce)");
    const update = () => setReduced(preference.matches);
    preference.addEventListener("change", update);
    return () => {
      preference.removeEventListener("change", update);
      if (openFallback.current !== null) window.clearTimeout(openFallback.current);
    };
  }, []);

  useEffect(() => {
    if (!ready.current || opening || closing) return;
    const flip = flipRef.current?.pageFlip();
    if (!flip) return;
    const target = 1 + pageIndex * 2;
    if (flip.getCurrentPageIndex() === target) { targetLeaf.current = null; return; }
    targetLeaf.current = target;
    if (!motion || reduced) { flip.turnToPage(target); targetLeaf.current = null; }
    else turnToward(target);
  }, [pageIndex, motion, reduced, opening, closing]);

  useEffect(() => {
    if (!closing || !ready.current || closed.current) return;
    const flip = flipRef.current?.pageFlip();
    if (!flip) return;
    if (flip.getCurrentPageIndex() === 0) {
      closed.current = true;
      onClosed?.();
    } else if (!motion || reduced) {
      flip.turnToPage(0);
      targetLeaf.current = null;
      closed.current = true;
      onClosed?.();
    } else {
      targetLeaf.current = 0;
      flip.getSettings().flippingTime = 280;
      turnToward(0);
    }
  }, [closing, motion, reduced, onClosed]);

  function handleInit() {
    ready.current = true;
    const flip = flipRef.current?.pageFlip();
    if (!flip) return;
    if (!motion || reduced) {
      flip.turnToPage(1);
      setOpening(false);
    } else {
      requestAnimationFrame(() => {
        openingTurnStarted.current = true;
        flip.flipNext();
      });
      // If the first turn is rejected while the window is still settling its
      // bounds, retry once, then reveal the first spread instead of trapping
      // the operator on the cover.
      openFallback.current = window.setTimeout(() => {
        if (flip.getCurrentPageIndex() !== 0) return;
        flip.flipNext();
        openFallback.current = window.setTimeout(() => {
          if (flip.getCurrentPageIndex() === 0) {
            flip.turnToPage(1);
            setOpening(false);
          }
        }, 1000);
      }, 1200);
    }
  }

  function handleFlip(event: { data: number }) {
    if (sound) playPageTurn();
    const flip = flipRef.current?.pageFlip();
    if (targetLeaf.current !== null && flip) {
      const target = targetLeaf.current;
      const current = flip.getCurrentPageIndex();
      if (current !== target) {
        turnToward(target);
        return;
      }
      targetLeaf.current = null;
    }
    if (event.data === 0) {
      setOpening(true);
      if (closing && !closed.current) {
        closed.current = true;
        onClosed?.();
      }
      return;
    }
    if (openFallback.current !== null) {
      window.clearTimeout(openFallback.current);
      openFallback.current = null;
    }
    const changedChapter = Math.floor((event.data - 1) / 2);
    if (changedChapter >= 0 && changedChapter < pages.length / 2) onPageChange?.(changedChapter);
  }

  function handleChangeState(event: { data: string }) {
    // PageFlip reports its page index as soon as the fold lands, just before
    // its renderer resets the curl. Keep the opening text hidden until it has
    // returned to the stable read state, so the first spread never shows
    // through a half-turned leaf.
    if (event.data !== "read" || !openingTurnStarted.current) return;
    const flip = flipRef.current?.pageFlip();
    if (!flip) return;
    if (flip.getCurrentPageIndex() === 0) {
      setOpening(true);
      return;
    }
    openingTurnStarted.current = false;
    if (openFallback.current !== null) {
      window.clearTimeout(openFallback.current);
      openFallback.current = null;
    }
    setOpening(false);
  }

  return <Theme appearance="light" accentColor="gold" grayColor="sand" radius="small" className="grimoire-theme">
    <div className="book-desk">
      <div className="book-toolbar"><span>ADAETUM <i> / </i> THE SANCTUARY ARCHITECT’S GRIMOIRE</span><div className="book-controls"><button type="button" aria-pressed={sound} aria-label={`Page turn sound ${sound ? "on" : "off"}`} title={`Page turn sound ${sound ? "on" : "off"}`} onClick={() => setSound(v => !v)}>{sound ? <Volume2 size={13} /> : <VolumeX size={13} />}</button><button type="button" disabled={reduced} aria-pressed={!motion || reduced} onClick={() => setMotion(v => !v)}>{reduced ? "Reduced motion" : motion ? "Pause page turns" : "Enable page turns"}</button></div></div>
      <div className={`setup grimoire ${className}`} role="region" aria-roledescription="book" aria-label={`Adaetum grimoire: ${title}`} data-opening={opening} data-closing={closing}>
        <div className="book-cover" aria-hidden="true" />
        <div className="page-stack" aria-hidden="true" />
        <div className="book-binding" aria-hidden="true" />
        <div className="book-ribbon" aria-hidden="true" />
        <HTMLFlipBook
          ref={flipRef as React.RefObject<never>}
          className="adaetum-pageflip"
          style={{ margin: "0 auto" }}
          width={460}
          height={688}
          startPage={0}
          size="stretch"
          minWidth={160}
          maxWidth={520}
          minHeight={300}
          maxHeight={850}
          showCover
          drawShadow
          startZIndex={0}
          flippingTime={950}
          clickEventForward
          usePortrait={false}
          autoSize
          maxShadowOpacity={0.42}
          mobileScrollSupport={false}
          swipeDistance={30}
          showPageCorners
          useMouseEvents={false}
          disableFlipByClick
          onInit={handleInit}
          onFlip={handleFlip}
          onChangeState={handleChangeState}
        >
          <BookLeaf key="front-cover" hard className="cover-leaf"><BookCover /></BookLeaf>
          {pages.map((content, index) => <BookLeaf key={`page-${index}`} className={index % 2 === 0 ? "left-leaf" : "right-leaf"}>{content}</BookLeaf>)}
          <BookLeaf key="back-cover" hard className="cover-leaf"><BookCover /></BookLeaf>
        </HTMLFlipBook>
        <div className="page-stack-edges" aria-hidden="true">
          {Array.from({ length: pages.length + 2 }, (_, index) => {
            const offset = index * 0.44;
            const sheet = index + 1;
            return <span key={index}>
              <i className="page-edge-bottom" data-sheet={sheet} style={{ bottom: -offset }} />
              <i className="page-edge-left" data-sheet={sheet} style={{ left: offset * 0.28 }} />
              <i className="page-edge-right" data-sheet={sheet} style={{ right: offset * 0.28 }} />
            </span>;
          })}
        </div>
        {chapters.length > 0 && <nav className="chapter-tabs" aria-label="Quest map">
          {chapters.map((item, i) => {
            const revealed = item.status === "done" || item.status === "running" || i === chapter;
            return <div key={item.id} className="tab-slot" data-side={i < 4 ? "right" : "left"} data-revealed={revealed}>
            {Array.from({ length: 6 }, (_, leaf) => <i key={leaf} className="tab-cutout-layer" aria-hidden="true" />)}
            <button type="button" className={`book-tab${i === chapter ? " current" : ""}`} data-side={i < 4 ? "right" : "left"} data-status={item.status} data-revealed={revealed} disabled={!item.enabled} aria-label={`Quest ${chapterNumerals[i]}: ${item.title}`} title={item.title} aria-current={i === chapter ? "page" : undefined} onClick={() => onChapter?.(item.id)}>
              <span className="tab-numeral">{chapterNumerals[i] ?? i + 1}</span><span className="tab-name">{item.title}</span>
            </button>
          </div>;
          })}
        </nav>}
        {footer && <div className="book-footer">{footer}</div>}
        <span className="book-folio left" aria-hidden="true">QUEST LOG</span>
        <span className="book-folio right" aria-hidden="true">{String((chapter + 1) * 2).padStart(2, "0")}</span>
      </div>
    </div>
  </Theme>;
}
