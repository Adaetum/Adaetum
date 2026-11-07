// Shared cover lettering keeps the resting book and its opening sheet visually
// continuous. Only public branding belongs here, never a form or operator data.
export default function BookCover() {
  return <div className="cover-inscription">
    <span className="cover-kicker">THE SANCTUARY ARCHITECT’S GRIMOIRE</span>
    <span className="cover-seal" aria-hidden="true">A</span>
    <h1 className="cover-title">Adaetum</h1>
    <span className="cover-subtitle">Prepare the sanctuary. Call the machines to service. Name an oracle to guide them.</span>
    <span className="cover-rule" aria-hidden="true" />
  </div>;
}
