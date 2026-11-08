import type { ToolCallMessagePartProps } from "@assistant-ui/react";
import { Badge, Button, Card, Code, Flex, Text } from "@radix-ui/themes";
import { useState } from "react";
import { StatusBadge } from "../StatusBadge";
import type { ToolCall } from "../../lib/assistantApi";
import type { ToolArtifact } from "../../lib/assistantMessages";
import { Tone } from "../../lib/status";

// What each tool does, in the words the page uses elsewhere.
const TOOL_LABEL: Record<string, string> = {
  search_tools: "Look for a tool",
  get_cluster: "Check the cluster",
  list_playbooks: "List playbooks",
  list_playbook_runs: "List recent launches",
  get_playbook_run: "Read a playbook's output",
  search_apps: "Search for an app",
  preview_app_install: "Preview adding an app",
  get_security_settings: "Read the security settings",
  launch_playbook: "Launch a playbook",
  cancel_playbook_run: "Stop the running playbook",
  run_host_command: "Run a command directly on a server",
  create_join_token: "Make a join token",
  approve_host: "Approve a machine",
  set_security_setting: "Change a security setting",
  remember: "Remember something",
  recall: "Look through memory",
  forget: "Forget a memory",
};

const TOOL_TONE: Record<ToolCall["status"], { label: string; tone: Tone }> = {
  pending: { label: "Waiting for you", tone: "amber" },
  running: { label: "Running", tone: "blue" },
  done: { label: "Done", tone: "green" },
  error: { label: "Failed", tone: "red" },
  denied: { label: "Not approved", tone: "gray" },
};

// launchSummary describes a playbook launch in words a non-expert can act on. The
// rehearse flag is read the same way the agent reads it: anything but an explicit
// `false` is a practice run, so the page can never call a real run harmless.
function launchSummary(tool: ToolCall): { real: boolean; title: string; detail: string } | null {
  if (tool.name !== "launch_playbook" || typeof tool.input !== "object" || tool.input === null) return null;
  const input = tool.input as { playbook?: unknown; servers?: unknown; rehearse?: unknown };
  const real = input.rehearse === false;
  const playbook = typeof input.playbook === "string" ? input.playbook : "a playbook";
  const servers = typeof input.servers === "string" && input.servers ? input.servers : "the chosen servers";
  return {
    real,
    title: real ? "Make the change for real" : "Practice run first",
    detail: real ? `This will run “${playbook}” on ${servers} and change them.` : `This will try “${playbook}” on ${servers} without changing anything, so you can see what it would do.`,
  };
}


// The agent's own status for a call, recovered from what the chat library holds.
function statusOf(props: ToolCallMessagePartProps): ToolCall["status"] {
  const approval = props.approval;
  if (approval && approval.approved === undefined && !approval.resolution) return "pending";
  if (approval?.approved === false) return "denied";
  if (props.result !== undefined) return props.isError ? "error" : "done";
  return "running";
}

// ToolCard is how every tool call shows in the conversation. Reading is automatic and
// only reported here; anything that changes something stops at "Waiting for you" and
// does nothing until Approve is pressed.
export function ToolCard(props: ToolCallMessagePartProps) {
  const [deciding, setDeciding] = useState(false);
  const status = statusOf(props);
  const tool: ToolCall = {
    id: props.toolCallId,
    name: props.toolName,
    input: props.args,
    mutates: (props.artifact as ToolArtifact | undefined)?.mutates ?? false,
    status,
    result: typeof props.result === "string" ? props.result : props.result === undefined ? undefined : JSON.stringify(props.result),
  };
  const label = TOOL_LABEL[tool.name] ?? tool.name;
  const state = TOOL_TONE[tool.status];
  const decide = async (approved: boolean) => {
    setDeciding(true);
    try {
      await props.respondToApproval({ approved });
    } finally {
      setDeciding(false);
    }
  };
  const input = JSON.stringify(tool.input, null, 2);
  const launch = launchSummary(tool);

  return (
    <Card size="1" style={{ borderLeft: tool.status === "pending" ? `3px solid var(--${launch?.real ? "red" : "amber"}-9)` : undefined }}>
      <Flex direction="column" gap="2">
        <Flex justify="between" align="center" gap="2" wrap="wrap">
          <Flex align="center" gap="2">
            <Text size="2" weight="medium">{launch ? launch.title : label}</Text>
            {launch ? (
              <Badge color={launch.real ? "red" : "green"} variant="surface" size="1">
                {launch.real ? "Changes the servers" : "Practice run, changes nothing"}
              </Badge>
            ) : (
              tool.mutates && <Badge color="gray" variant="surface" size="1">Changes something</Badge>
            )}
          </Flex>
          <StatusBadge tone={state.tone}>{state.label}</StatusBadge>
        </Flex>
        {launch && <Text size="2" color="gray">{launch.detail}</Text>}
        {/* A launch is explained above in plain words; the exact settings stay one click away. */}
        {input !== "{}" &&
          (launch ? (
            <details>
              <summary><Text size="1" color="gray">Exact settings</Text></summary>
              <Code size="1" style={{ whiteSpace: "pre-wrap", display: "block", overflowWrap: "anywhere" }}>{input}</Code>
            </details>
          ) : (
            <Code size="1" style={{ whiteSpace: "pre-wrap", display: "block", overflowWrap: "anywhere" }}>{input}</Code>
          ))}
        {tool.status === "pending" && (
          <Flex gap="2" align="center" wrap="wrap">
            <Button color={launch && !launch.real ? undefined : "red"} variant="soft" highContrast onClick={() => void decide(true)} disabled={deciding}>
              {launch ? (launch.real ? "Approve and make the change" : "Start the practice run") : "Approve"}
            </Button>
            <Button variant="soft" color="gray" onClick={() => void decide(false)} disabled={deciding}>Deny</Button>
            <Text size="1" color="gray">{launch && !launch.real ? "A practice run shows what would change and changes nothing." : "Nothing happens until you approve."}</Text>
          </Flex>
        )}
        {tool.result && tool.status !== "pending" && (
          <details>
            <summary><Text size="1" color="gray">Result</Text></summary>
            <Code size="1" style={{ whiteSpace: "pre-wrap", display: "block", overflowWrap: "anywhere", maxHeight: "14rem", overflow: "auto" }} tabIndex={0}>
              {tool.result}
            </Code>
          </details>
        )}
      </Flex>
    </Card>
  );
}
