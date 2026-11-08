import type { ThreadMessageLike } from "@assistant-ui/react";
import { ConversationState, Item, ToolCall } from "./assistantApi";

// The agent keeps a flat transcript (user, assistant, tool, notice and error items). The
// chat library shows turns: one message per person, and one per assistant turn holding
// text, tool calls and notes in order. This is the whole translation, so the library
// never needs to know the agent's own shape.

type Part = Exclude<ThreadMessageLike["content"], string>[number];

// What the tool card needs that a plain tool-call part has no field for.
export interface ToolArtifact {
  mutates: boolean;
}

function toolPart(tool: ToolCall): Part {
  const part = {
    type: "tool-call" as const,
    toolCallId: tool.id,
    toolName: tool.name,
    args: (typeof tool.input === "object" && tool.input !== null ? tool.input : {}) as Record<string, never>,
    artifact: { mutates: tool.mutates } satisfies ToolArtifact,
  };
  switch (tool.status) {
    // The library's approval gate: shown until the person answers, then resolved.
    case "pending":
      return { ...part, approval: { id: tool.id } };
    case "denied":
      return { ...part, approval: { id: tool.id, approved: false } };
    case "done":
      return { ...part, result: tool.result ?? "" };
    case "error":
      return { ...part, result: tool.result ?? "", isError: true };
    default:
      // Running: no result yet.
      return part;
  }
}

function itemPart(item: Item): Part | null {
  if (item.kind === "tool" && item.tool) return toolPart(item.tool);
  if (item.kind === "error" || item.kind === "notice") {
    return { type: "data-note", data: { text: item.text ?? "", error: item.kind === "error" } };
  }
  if (item.kind === "assistant") return { type: "text", text: item.text ?? "" };
  return null;
}

export function toMessages(items: Item[], state: ConversationState): ThreadMessageLike[] {
  const messages: { role: "user" | "assistant"; id: string; parts: Part[] }[] = [];
  for (const item of items) {
    if (item.kind === "user") {
      messages.push({ role: "user", id: `u${item.seq}`, parts: [{ type: "text", text: item.text ?? "" }] });
      continue;
    }
    const part = itemPart(item);
    if (!part) continue;
    const last = messages[messages.length - 1];
    if (last && last.role === "assistant") last.parts.push(part);
    else messages.push({ role: "assistant", id: `a${item.seq}`, parts: [part] });
  }
  return messages.map((message, index): ThreadMessageLike => {
    if (message.role === "user") return { role: "user", id: message.id, content: message.parts };
    const newest = index === messages.length - 1;
    return {
      role: "assistant",
      id: message.id,
      content: message.parts,
      status:
        newest && state === "working"
          ? { type: "running" }
          : newest && state === "waiting_approval"
            ? { type: "requires-action", reason: "tool-calls" }
            : { type: "complete", reason: "stop" },
    };
  });
}
