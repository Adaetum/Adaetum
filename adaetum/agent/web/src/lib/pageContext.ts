// What the person is looking at, as text the assistant can read. A page may describe
// itself in a few exact lines (useAssistantPage); any page that does not is read from the
// screen instead, so the assistant is never blind to a page nobody has taught it about.
//
// Mark anything that must never be shared with data-private; it is removed before the
// text is read (a join token shown once, for example).

const MAX_SCREEN_TEXT = 4000;

export function describeScreen(registered: string | null): string {
  const title = document.querySelector("main h1")?.textContent?.trim() || "this page";
  const head = `Screen: ${title} (${window.location.hash || "#/overview"})`;
  if (registered) return `${head}\n${registered}`;
  const main = document.getElementById("main");
  if (!main) return head;
  const copy = main.cloneNode(true) as HTMLElement;
  copy.querySelectorAll("[data-private]").forEach((node) => node.remove());
  // A clone is not laid out, so innerText would run everything together; textContent
  // with the structure's own whitespace is enough for a model to read.
  const text = (copy.textContent ?? "").replace(/\s{3,}/g, "\n").trim();
  return text ? `${head}\nWhat is on screen:\n${text.slice(0, MAX_SCREEN_TEXT)}` : head;
}
