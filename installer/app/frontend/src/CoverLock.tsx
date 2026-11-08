type LockPhase = "idle" | "shake" | "unlocking" | "relocking";

// The same physical pieces serve both directions of the cover transition.
export default function CoverLock({ phase, onAnimationEnd }: { phase: LockPhase; onAnimationEnd?: () => void }) {
  return <>
    <img className="cover-lock-eye" src={new URL("./art/lock-eye.svg", import.meta.url).href} alt="" aria-hidden="true" />
    <div className="cover-lock" data-unlock-stage={phase} aria-hidden="true" onAnimationEnd={(event) => {
      if (event.target === event.currentTarget) onAnimationEnd?.();
    }}>
      <img className="lock-strap" src={new URL("./art/lock-strap.svg", import.meta.url).href} alt="" />
      <img className="lock-shackle" src={new URL("./art/lock-shackle.svg", import.meta.url).href} alt="" />
      <img className="lock-body" src={new URL("./art/lock-body.svg", import.meta.url).href} alt="" />
    </div>
  </>;
}
