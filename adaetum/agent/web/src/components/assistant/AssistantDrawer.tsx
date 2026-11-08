import {
  AssistantRuntimeProvider,
  AuiIf,
  ComposerPrimitive,
  MessagePrimitive,
  ThreadPrimitive,
  useExternalStoreRuntime,
  type ThreadMessageLike,
} from "@assistant-ui/react";
import { MarkdownTextPrimitive } from "@assistant-ui/react-markdown";
import { Button, Callout, IconButton, Skeleton, Text, Tooltip } from "@radix-ui/themes";
import { Bot, CircleAlert, Eye, EyeOff, RotateCcw, Square, X } from "lucide-react";
import { useMemo } from "react";
import remarkGfm from "remark-gfm";
import { AssistantStatus, ConversationHook } from "../../lib/assistantApi";
import { toMessages } from "../../lib/assistantMessages";
import { ROUTES, useLocation } from "../../app/routes";
import { setSharePage, useSharePage } from "../../lib/preferences";
import { useToken } from "../../lib/auth";
import { Login } from "../../pages/Login";
import { ToolCard } from "./ToolCard";
import "./assistant.css";

// The assistant's side drawer. The conversation itself lives in the agent and is read
// by lib/assistantApi; this file only lays it out. assistant-ui supplies the thread
// behaviour (scrolling, composer, tool-call and approval states, markdown); we supply
// what appears: the card for each tool call, and the notes.
//
// Whatever the assistant can draw must be a component named here. The agent sends data
// and a name, never markup, so nothing it says can add a script or a link to the page.

export interface DrawerProps {
  conversation: ConversationHook;
  status: AssistantStatus | null;
  statusError: string | null;
  // What is on screen, to send with a message ("" when the person has turned that off).
  page: () => string;
  onClose: () => void;
}

const EXAMPLES = [
  "Which servers don't have the agent yet?",
  "Rehearse installing the agent on node-50449",
  "Remember that node-b reboots on Tuesdays",
];

export default function AssistantDrawer({ conversation, status, statusError, page, onClose }: DrawerProps) {
  const token = useToken();
  const { items, authRequired, signedIn, reset } = conversation;

  return (
    <aside className="assistant-drawer" aria-label="Assistant">
      <header className="assistant-head">
        <Bot size={18} aria-hidden />
        <Text size="3" weight="bold" style={{ flex: 1 }}>Assistant</Text>
        <Tooltip content="New conversation">
          <IconButton variant="ghost" color="gray" size="2" aria-label="New conversation" onClick={reset} disabled={items.length === 0}>
            <RotateCcw size={15} />
          </IconButton>
        </Tooltip>
        <Tooltip content="Close">
          <IconButton variant="ghost" color="gray" size="2" aria-label="Close the assistant" onClick={onClose}>
            <X size={16} />
          </IconButton>
        </Tooltip>
      </header>

      {authRequired ? (
        <div className="assistant-pad"><Login rejected={token !== ""} onSignedIn={signedIn} /></div>
      ) : statusError ? (
        <div className="assistant-pad">
          <Callout.Root color="red" role="alert">
            <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
            <Callout.Text>{statusError}</Callout.Text>
          </Callout.Root>
        </div>
      ) : !status ? (
        <div className="assistant-pad"><Skeleton height="120px" /></div>
      ) : !status.enabled ? (
        <div className="assistant-pad">
          <Callout.Root color="gray" role="status">
            <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
            <Callout.Text>{status.reason} <a href="#/settings?tab=ai-model" onClick={onClose}>Set it up on the AI model page.</a></Callout.Text>
          </Callout.Root>
        </div>
      ) : (
        <Thread conversation={conversation} status={status} page={page} />
      )}
    </aside>
  );
}

function Thread({ conversation, status, page }: { conversation: ConversationHook; status: AssistantStatus; page: () => string }) {
  const { items, state, error, send, decide, stop } = conversation;
  const messages = useMemo(() => toMessages(items, state), [items, state]);

  const runtime = useExternalStoreRuntime<ThreadMessageLike>({
    messages,
    convertMessage: (message) => message,
    isRunning: state === "working",
    // A reply is not accepted while a change waits for an answer, so the box says so
    // rather than failing after the fact.
    isSendDisabled: state !== "idle",
    onNew: async (message) => {
      const text = message.content.map((part) => (part.type === "text" ? part.text : "")).join("\n").trim();
      if (text) await send(text, page());
    },
    onCancel: async () => stop(),
    onRespondToToolApproval: async ({ approvalId, approved }) => decide(approvalId, approved),
  });

  return (
    <AssistantRuntimeProvider runtime={runtime}>
      <ThreadPrimitive.Root className="assistant-thread">
        <Text size="1" color="gray" className="assistant-note">
          Powered by {status.model} ({status.provider ?? "an outside AI service"}{status.endpoint ? `, ${status.endpoint}` : ""}). What you type and what it reads from the agent is sent there. Changes need your approval.
        </Text>
        <ThreadPrimitive.Viewport className="assistant-viewport">
          <ThreadPrimitive.Empty>
            <div className="assistant-empty">
              <Text size="2" color="gray">Ask about your cluster, or ask it to do something. Try:</Text>
              {EXAMPLES.map((example) => (
                <Text key={example} size="2" color="gray">“{example}”</Text>
              ))}
            </div>
          </ThreadPrimitive.Empty>
          <ThreadPrimitive.Messages>
            {({ message }) => (message.role === "user" ? <UserMessage /> : <AssistantMessage />)}
          </ThreadPrimitive.Messages>
          {state === "working" && <Text size="2" color="gray" className="assistant-working">Working…</Text>}
        </ThreadPrimitive.Viewport>

        {error && (
          <Callout.Root color="red" size="1" role="alert" className="assistant-error">
            <Callout.Icon><CircleAlert size={14} /></Callout.Icon>
            <Callout.Text>{error}</Callout.Text>
          </Callout.Root>
        )}

        <ComposerPrimitive.Root className="assistant-composer">
          <PageChip />
          <ComposerPrimitive.Input
            className="assistant-input"
            rows={2}
            placeholder={state === "waiting_approval" ? "Approve or deny the change above first" : "Message the assistant"}
            aria-label="Message the assistant"
          />
          <div className="assistant-actions">
            <AuiIf condition={(s) => s.thread.isRunning}>
              <ComposerPrimitive.Cancel asChild>
                <Button type="button" variant="soft" color="gray"><Square size={12} aria-hidden /> Stop</Button>
              </ComposerPrimitive.Cancel>
            </AuiIf>
            <ComposerPrimitive.Send asChild>
              <Button type="submit">Send</Button>
            </ComposerPrimitive.Send>
          </div>
        </ComposerPrimitive.Root>
      </ThreadPrimitive.Root>
    </AssistantRuntimeProvider>
  );
}

// Says, and lets the person switch off, that what is on screen goes with each message.
// It is on by default because "this" and "here" are what people say, but it is never
// silent: the chip is always there while it is on.
function PageChip() {
  const share = useSharePage();
  const location = useLocation();
  const label = ROUTES.find((route) => route.id === location.path)?.label ?? "this page";
  return (
    <button
      type="button"
      className="assistant-chip"
      aria-pressed={share}
      onClick={() => setSharePage(!share)}
      title={share ? "What is on screen is sent with each message so you can say “this”. Click to stop sharing it." : "Click to send what is on screen with each message."}
    >
      {share ? <Eye size={12} aria-hidden /> : <EyeOff size={12} aria-hidden />}
      {share ? `Using this page: ${label}` : "Not sharing this page"}
      {share && <X size={12} aria-hidden />}
    </button>
  );
}

function UserMessage() {
  return (
    <MessagePrimitive.Root className="assistant-row assistant-user">
      <div className="assistant-bubble">
        <MessagePrimitive.Parts components={{ Text: PlainText }} />
      </div>
    </MessagePrimitive.Root>
  );
}

function AssistantMessage() {
  return (
    <MessagePrimitive.Root className="assistant-row">
      <MessagePrimitive.Parts
        components={{
          Text: MarkdownText,
          tools: { Fallback: ToolCard },
          data: { by_name: { note: Note } },
        }}
      />
    </MessagePrimitive.Root>
  );
}

// What a person typed is shown as typed; only the assistant's replies are markdown.
function PlainText({ text }: { text: string }) {
  return <Text size="2" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{text}</Text>;
}

function MarkdownText() {
  // The agent delivers whole replies between polls, so the typing animation would only
  // delay text that is already there.
  return <MarkdownTextPrimitive className="assistant-md" remarkPlugins={[remarkGfm]} smooth={false} />;
}

function Note({ data }: { data: { text: string; error: boolean } }) {
  return (
    <Callout.Root color={data.error ? "red" : "gray"} size="1" role={data.error ? "alert" : "status"}>
      <Callout.Icon><CircleAlert size={14} /></Callout.Icon>
      <Callout.Text>{data.text}</Callout.Text>
    </Callout.Root>
  );
}
