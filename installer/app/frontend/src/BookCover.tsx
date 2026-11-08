import { Button } from "@radix-ui/themes";
import { BookOpen, Rocket } from "lucide-react";

// Shared cover lettering keeps the resting book and its opening sheet visually
// continuous. Only public branding belongs here, never a form or operator data.
export default function BookCover({ resume = false, onStart, disabled = false }: { resume?: boolean; onStart?: () => void; disabled?: boolean }) {
  return <><div className="cover-inscription">
    <span className="cover-kicker">THE SANCTUARY ARCHITECT’S GRIMOIRE</span>
    <span className="cover-seal" aria-hidden="true">A</span>
    <h1 className="cover-title">Adaetum</h1>
    <span className="cover-subtitle">Prepare the sanctuary. Call the machines to service. Name an oracle to guide them.</span>
    <span className="cover-rule" aria-hidden="true" />
  </div>
    <p className="closed-intro">Configure your installer ISO, install your private cloud on your servers, and connect an LLM to manage it.</p>
    <div className="actions welcome-actions">
      <Button size="3" onClick={onStart} disabled={disabled} tabIndex={onStart ? 0 : -1}>{resume ? <BookOpen size={16} /> : <Rocket size={16} />}{resume ? "Reopen your Adaetum" : "Create your Adaetum"}</Button>
    </div>
  </>;
}
