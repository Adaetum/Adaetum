import { createContext, forwardRef, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import HTMLFlipBook from "react-pageflip";
import { Button, Theme } from "@radix-ui/themes";
import { Check, Copy } from "lucide-react";
import { chapterNumerals } from "./journey";
import "./Book.css";
import BookCover from "./BookCover";
import CoverLock from "./CoverLock";

export type ChapterTab = { id: string; title: string; status: string; enabled?: boolean };

export function BookHandoff({ isoPath, copyPath, instructions, simulation = false }: { isoPath: string; copyPath?: string; instructions: string[]; simulation?: boolean }) {
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
          <div className="handoff-book-cover" />
        </div>
        <aside className="handoff-note" aria-labelledby="handoff-title">
          <div className="handoff-note-heading"><span>FINISHED · SETUP COMPLETE</span><span>VIII</span></div>
          <h1 id="handoff-title">Your installer is ready</h1>
          <p className="handoff-note-intro">{simulation ? "This is an example handoff from the UI rehearsal. No installer was built or copied." : "The setup is complete. Keep this note with your installer ISO."}</p>
          <div className="handoff-path"><span>{simulation ? "EXAMPLE ISO PATH" : copyPath ? "ISO · DOWNLOADS COPY" : "INSTALLER ISO"}</span><code>{copyPath || isoPath}</code></div>
          {copyPath && <div className="handoff-path secondary"><span>ORIGINAL ISO</span><code>{isoPath}</code></div>}
          <h2>When you’re ready</h2>
          <ol className="handoff-instructions">{instructions.map((instruction, i) => <li key={i}><span>{i + 1}</span><p>{instruction}</p></li>)}</ol>
          <Button size="2" variant="soft" disabled={simulation} onClick={copyIsoPath}>{copied ? <Check size={14} /> : <Copy size={14} />}{simulation ? "Example path only" : copied ? "Copied path" : "Copy ISO path"}</Button>
        </aside>
      </div>
    </main>
  </Theme>;
}

type FlipApi = { getCurrentPageIndex: () => number; getState: () => string; getSettings: () => { flippingTime: number; width: number; height: number }; update: () => void; flip: (page: number) => void; flipNext: () => void; flipPrev: () => void; turnToPage: (page: number) => void };
type FlipHandle = { pageFlip: () => FlipApi };
type LeafContent = { pages: ReactNode[]; chapters: ChapterTab[]; activeChapter: number; onChapter?: (id: string) => void };
const PageContent = createContext<LeafContent>({ pages: [], chapters: [], activeChapter: 0 });
type BookLeafProps = { children?: ReactNode; pageIndex?: number; className?: string; hard?: boolean };
const BookLeaf = forwardRef<HTMLDivElement, BookLeafProps>(function BookLeaf({ children, pageIndex, className = "", hard = false }, ref) {
  const { pages, chapters, activeChapter, onChapter } = useContext(PageContent);
  const leafChapter = pageIndex === undefined ? -1 : Math.floor(pageIndex / 2);
  const side = pageIndex === undefined ? "" : pageIndex % 2 === 0 ? "left" : "right";
  const tabs = chapters.map((item, i) => ({ item, i })).filter(({ i }) => side === "left" ? i < leafChapter : side === "right" && i >= leafChapter);
  return <div ref={ref} className={`grimoire-page ${className}`} data-density={hard ? "hard" : "soft"}>
    {pageIndex === undefined ? children : pages[pageIndex]}
    {/* Edge controls live on the sheet so PageFlip carries and clips them during a turn. */}
    {tabs.length > 0 && <nav className="leaf-tabs" aria-label={side === "left" ? "Previous quests" : "Current and upcoming quests"}>
      {tabs.map(({ item, i }) => {
        const revealed = i <= leafChapter;
        const top = `${11 + (65 * i) / Math.max(1, chapters.length - 1)}%`;
        return <div key={item.id} className="tab-slot" data-side={side} data-revealed={revealed} style={{ top }}>
          {Array.from({ length: 6 }, (_, leaf) => <i key={leaf} className="tab-cutout-layer" aria-hidden="true" />)}
          <button type="button" className={`book-tab${i === leafChapter ? " current" : ""}`} data-side={side} data-status={item.status} data-revealed={revealed} disabled={!item.enabled} aria-label={`Quest ${chapterNumerals[i]}: ${item.title}`} title={item.title} aria-current={i === activeChapter && i === leafChapter ? "page" : undefined} onClick={() => onChapter?.(item.id)}>
            <span className="tab-numeral">{chapterNumerals[i] ?? i + 1}</span><span className="tab-name">{item.title}</span>
          </button>
        </div>;
      })}
    </nav>}
  </div>;
});

// PageFlip owns only physical page movement. Adaetum's engine and React pages
// continue to own all setup state, questions and provider interactions.
export default function Book({ pageIndex, chapter, title, className = "", chapters = [], onChapter, onPageChange, footer, closing = false, returnToCover = false, onClosed, pages }: {
  pageIndex: number;
  chapter: number;
  title: string;
  className?: string;
  chapters?: ChapterTab[];
  onChapter?: (id: string) => void;
  onPageChange?: (pageIndex: number) => void;
  footer?: ReactNode;
  closing?: boolean;
  returnToCover?: boolean;
  onClosed?: () => void;
  pages: ReactNode[];
}) {
  const flipRef = useRef<FlipHandle | null>(null);
  const spreadRef = useRef<HTMLDivElement | null>(null);
  const fittedSize = useRef("");

  // The flexible frame owns both dimensions. Give PageFlip the same ratio so
  // its physical leaves stay aligned with the backing when the window resizes.
  function fitLeaves() {
    const spread = spreadRef.current;
    const flip = flipRef.current?.pageFlip();
    if (!spread || !flip || !spread.clientWidth || !spread.clientHeight) return;
    if (flip.getState() !== "read") return;
    const size = `${spread.clientWidth}:${spread.clientHeight}`;
    if (fittedSize.current === size) return;
    fittedSize.current = size;
    Object.assign(flip.getSettings(), { width: spread.clientWidth / 2, height: spread.clientHeight });
    flip.update();
  }

  useEffect(() => {
    const spread = spreadRef.current;
    if (!spread) return;
    const observer = new ResizeObserver(fitLeaves);
    observer.observe(spread);
    return () => observer.disconnect();
  }, []);
  const [motion, setMotion] = useState(true);
  const [reduced, setReduced] = useState(() => window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  const [opening, setOpening] = useState(true);
  const [coverCentered, setCoverCentered] = useState(true);
  const [coverDocked, setCoverDocked] = useState(false);
  const [relocking, setRelocking] = useState(false);
  const ready = useRef(false);
  const closed = useRef(false);
  const returnedToCover = useRef(false);
  const relockStarted = useRef(false);
  const openingTurnStarted = useRef(false);
  const coverTurnLanded = useRef(false);
  const targetLeaf = useRef<number | null>(null);
  const openFallback = useRef<number | null>(null);
  const slideTimer = useRef<number | null>(null);
  const closeFallback = useRef<number | null>(null);
  // react-pageflip rebuilds its DOM pages whenever its children prop changes.
  // Keep the physical leaves stable while live questions and progress update.
  const flipLeaves = useMemo(() => [
    <BookLeaf key="front-cover" hard className="cover-leaf front-cover-leaf"><BookCover /></BookLeaf>,
    ...Array.from({ length: pages.length }, (_, index) => <BookLeaf key={`page-${index}`} pageIndex={index} className={index % 2 === 0 ? "left-leaf" : "right-leaf"} />),
    <BookLeaf key="back-cover" hard className="cover-leaf back-cover-leaf" />,
  ], [pages.length]);
  // PageFlip registers callbacks when leaves mount. Read current controls and
  // navigation from a ref without re-registering during a turn.
  const liveEvents = useRef({ closing, returnToCover, onClosed, onPageChange, pageCount: pages.length });
  liveEvents.current = { closing, returnToCover, onClosed, onPageChange, pageCount: pages.length };

  function finishReturnToCover() {
    if (returnedToCover.current) return;
    returnedToCover.current = true;
    liveEvents.current.onClosed?.();
  }

  function finishCoverSlide() {
    if (relockStarted.current) return;
    relockStarted.current = true;
    setRelocking(true);
  }

  useEffect(() => {
    if (!coverDocked) return;
    // Transition-end normally completes the slide. This fallback also covers
    // reduced motion and a hidden window where no transition event is delivered.
    const timer = window.setTimeout(finishCoverSlide, reduced ? 0 : 520);
    return () => window.clearTimeout(timer);
  }, [coverDocked, reduced]);

  useEffect(() => {
    if (!relocking) return;
    // The animation event is primary; keep the cover usable if it is suppressed.
    const timer = window.setTimeout(finishReturnToCover, reduced ? 0 : 850);
    return () => window.clearTimeout(timer);
  }, [relocking, reduced]);

  function turnToward(target: number) {
    const flip = flipRef.current?.pageFlip();
    if (!flip) return;
    if (flip.getCurrentPageIndex() === target || flip.getState() !== "read") return;
    // PageFlip's target flip animates the destination sheet, even for a tab
    // jump across several chapters.
    flip.flip(target);
  }

  useEffect(() => {
    const preference = window.matchMedia("(prefers-reduced-motion: reduce)");
    const update = () => setReduced(preference.matches);
    preference.addEventListener("change", update);
    return () => {
      preference.removeEventListener("change", update);
      if (openFallback.current !== null) window.clearTimeout(openFallback.current);
      if (slideTimer.current !== null) window.clearTimeout(slideTimer.current);
      if (closeFallback.current !== null) window.clearTimeout(closeFallback.current);
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
      if (returnToCover) setCoverDocked(true);
      else onClosed?.();
    } else if (!motion || reduced) {
      flip.turnToPage(0);
      targetLeaf.current = null;
      closed.current = true;
      onClosed?.();
    } else {
      targetLeaf.current = 0;
      flip.getSettings().flippingTime = returnToCover ? 950 : 280;
      // The first spread is adjacent to the front cover. A direct previous
      // turn leaves PageFlip's visible cover in sync with its page index.
      if (returnToCover && flip.getCurrentPageIndex() === 1) flip.flipPrev();
      else turnToward(0);
      if (returnToCover) closeFallback.current = window.setTimeout(() => {
        if (coverTurnLanded.current || closed.current) return;
        flip.turnToPage(0);
        closed.current = true;
        setCoverDocked(true);
      }, 1400);
    }
  }, [closing, motion, reduced, returnToCover, onClosed]);

  function handleInit() {
    ready.current = true;
    fitLeaves();
    const flip = flipRef.current?.pageFlip();
    if (!flip) return;
    if (!motion || reduced) {
      setCoverCentered(false);
      flip.turnToPage(1);
      setOpening(false);
    } else {
      // PageFlip's closed cover begins in the right half of its spread. Start
      // the whole spread one quarter-width left, then slide it into place.
      // Two frames allow the centered cover to paint before the transform changes.
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          setCoverCentered(false);
          slideTimer.current = window.setTimeout(() => {
            openingTurnStarted.current = true;
            flip.flipNext();
            // If the first turn is rejected while the window is settling,
            // retry once and then show the first spread.
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
          }, 450);
        });
      });
    }
  }

  function handleFlip(event: { data: number }) {
    if (targetLeaf.current !== null) {
      // A newer tab choice made during this turn waits for read state. Do not
      // let the older landing page replace that choice in the parent.
      if (event.data !== targetLeaf.current) return;
      targetLeaf.current = null;
    }
    if (event.data === 0) {
      if (liveEvents.current.closing) coverTurnLanded.current = true;
      setOpening(true);
      return;
    }
    if (openFallback.current !== null) {
      window.clearTimeout(openFallback.current);
      openFallback.current = null;
    }
    const changedChapter = Math.floor((event.data - 1) / 2);
    if (changedChapter >= 0 && changedChapter < liveEvents.current.pageCount / 2) liveEvents.current.onPageChange?.(changedChapter);
  }

  function handleChangeState(event: { data: string }) {
    // PageFlip reports the new page before it resets the curl. Keep the static
    // backing clipped until read state; the moving leaf reveals its contents
    // progressively while the cover turns.
    if (event.data !== "read") return;
    // A resize received during a turn is applied after PageFlip finishes it.
    requestAnimationFrame(fitLeaves);
    const flip = flipRef.current?.pageFlip();
    if (!flip) return;
    if (liveEvents.current.closing && coverTurnLanded.current && flip.getCurrentPageIndex() === 0 && !closed.current) {
      closed.current = true;
      if (closeFallback.current !== null) window.clearTimeout(closeFallback.current);
      if (liveEvents.current.returnToCover) setCoverDocked(true);
      else window.requestAnimationFrame(() => liveEvents.current.onClosed?.());
      return;
    }
    if (!openingTurnStarted.current) {
      // The library emits "read" before updating getState(). Start a queued
      // destination flip on the next frame, after that state change completes.
      if (targetLeaf.current !== null) window.requestAnimationFrame(() => {
        if (targetLeaf.current !== null) turnToward(targetLeaf.current);
      });
      return;
    }
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
      <div className="book-toolbar"><span>ADAETUM <i> / </i> THE SANCTUARY ARCHITECT’S GRIMOIRE</span><div className="book-controls"><button type="button" disabled={reduced} aria-pressed={!motion || reduced} onClick={() => setMotion(v => !v)}>{reduced ? "Reduced motion" : motion ? "Pause page turns" : "Enable page turns"}</button></div></div>
      <div ref={spreadRef} className={`setup grimoire ${className}`} role="region" aria-roledescription="book" aria-label={`Adaetum grimoire: ${title}`} data-opening={opening} data-cover-centered={coverCentered} data-closing={closing} data-returning-cover={returnToCover} data-cover-docked={coverDocked} onTransitionEnd={(event) => {
        if (coverDocked && event.target === event.currentTarget && event.propertyName === "transform") finishCoverSlide();
      }}>
        <div className="book-cover" aria-hidden="true" />
        <div className="page-stack" aria-hidden="true" />
        <div className="book-binding" aria-hidden="true" />
        <div className="book-ribbon" aria-hidden="true" />
        {coverDocked && <div className="travel-cover" aria-hidden="true"><BookCover />{relocking && <CoverLock phase="relocking" onAnimationEnd={finishReturnToCover} />}</div>}
        <PageContent.Provider value={{ pages, chapters, activeChapter: chapter, onChapter }}>
          <HTMLFlipBook
          ref={flipRef as React.RefObject<never>}
          className="adaetum-pageflip"
          style={{ margin: "0 auto" }}
          width={460}
          height={690}
          startPage={0}
          size="stretch"
          minWidth={160}
          maxWidth={520}
          minHeight={240}
          maxHeight={780}
          showCover
          drawShadow
          startZIndex={0}
          flippingTime={950}
          clickEventForward
          usePortrait={false}
          autoSize={false}
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
          {flipLeaves}
          </HTMLFlipBook>
        </PageContent.Provider>
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
        {footer && <div className="book-footer">{footer}</div>}
        <span className="book-folio left" aria-hidden="true">QUEST LOG</span>
        <span className="book-folio right" aria-hidden="true">{String((chapter + 1) * 2).padStart(2, "0")}</span>
      </div>
    </div>
  </Theme>;
}
