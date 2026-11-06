# Memory and the assistant

Two features that go together: the agent can **remember** things and find them again
by meaning, and an **assistant** in the web page (and any MCP client) can do
everything the page can do, using that memory.

## Memory (`internal/memory`)

- **Saving is a plain write.** A memory is stored in the cluster agent's Turso
  database with a row in a durable job queue. It never waits for, or depends on, an
  embedding, so a slow, failing, or missing embedder cannot block a person, a
  playbook, or the assistant.
- **A worker drains the queue** in the background: ask the embedder for vectors, store
  them beside the memory, retry failures with growing delays (5 s up to 15 min), park
  a job as *failed* after 8 tries. A person can retry parked jobs from the Memory page
  once the service is fixed.
- **Recall stays in the Go backend.** Search embeds the question, ranks stored vectors
  by cosine similarity, and falls back to keyword matching when the embedder is down
  or a memory has no vector yet. A just-saved memory is findable at once, by keyword.
  Vectors record their model, and vectors from different models are never compared.
- **Secrets are refused.** Recalled memories are sent to an AI service, so text that
  looks like a password, key or token is not stored.
- **The Memory page** lists, searches, adds and forgets memories, and says plainly
  when search has fallen back to keywords.

### The embedder, and the Embedding page

`memory.Embedder` is one method, and what sits behind it is a setting you change on the
**Embedding** page (saved in the database, so it survives restarts and wins over the
startup flags):

| Choice | What it is | Leaves the cluster? |
|---|---|---|
| **The embedding service in the cluster** | The dedicated pod (`embed/`): Go, MiniLM-L6-v2 INT8 through ONNX Runtime, 1 to 2 cores, one replica, no state. Recommended. | No |
| **Another service** | Any outside service speaking the OpenAI embeddings API. The key comes only from `-embedder-key-file` (never stored or shown), and you must confirm that memories will be sent there. | **Yes** |
| **Built in** | A lexical stand-in (hashed words). Not semantic. For trying things out. | No |
| **Off** | Keyword search only. | No |

Every choice is **tested before it is adopted** (it must work, and the page says whether
it really understands meaning: a sentence about a failing disk must score clearly nearer
"hard disk hardware faults" than an unrelated one), and a change never loses a memory.
Vectors record their model and are only compared with their own kind, so after a change
the old ones stop counting until **Re-embed** makes new ones. Addresses with credentials,
link-local or cloud-metadata hosts are refused. The assistant can switch between the
in-cluster choices but can never choose an outside service: sending memories out is a
person's decision.

### The pod (`embed/`)

Built and verified with the real model on macOS (see `embed/README.md`): the tokenizer
matches the real vocabulary's ids, the INT8 model runs through ONNX Runtime 1.31, and a
sentence about a failing storage drive scored 0.36 against a hard-disk-faults sentence
with no words in common and about 0.03 against unrelated text. One sequence is embedded
per run, because the INT8 model's dynamic quantisation would otherwise make a vector
depend on its batch.

**Not verified:** the `Dockerfile` (no container runtime was available), the Linux x86_64
model and runtime together, and the manifests against a live cluster. The manifests
(`embed/deploy/`) are not in `pods/` yet: that needs a published image, and nothing in the
repository builds this one.

Not built either: host agents submitting memories through their event outbox (today
memories are written to the cluster agent's API).

## The assistant (`internal/assistant`)

A conversation inside the cluster agent's page. **Off by default**: it sends the
conversation and everything the assistant reads from the agent to an outside AI
service, so it only runs once someone picks the service and switches it on.

**Setting it up on the fly.** The **AI model** page chooses the service, model and
(for outside services) key, tests the choice, and switches the assistant on or off with
no restart. "Use this" tests first and only adopts a setup that works; an outside
service also needs an explicit confirmation that conversations will leave the machine.
The provider, address, model **and the key** are saved in the cluster database, so
they survive restarts and are shared by everyone using this cluster agent. That is a
deliberate choice: the database sits in the agent's private directory (mode 700), so
reading it already needs host-level access. The key is never returned by any API or
logged (the page only shows where it came from), it is dropped when the provider
changes, and "Delete saved key" removes it for good. A key the agent was started with
(`-assistant-key-file`, mode 600, or `ANTHROPIC_API_KEY` / `ASSISTANT_API_KEY`) is the
fallback. If the database ever leaves that protection (backups, a shared volume),
encrypt the key or move it to OpenBao. The assistant has no tool for any of this: it
cannot choose where its own conversations go.
`-enable-assistant`, `-assistant-provider`, `-assistant-url` and `-assistant-model`
only set the starting choice; a saved choice wins.

**The services** (the page's choices; the flags take the same ids):

| Provider | Use | Key | Notes |
|---|---|---|---|
| `anthropic` (default) | the real thing; official Go SDK, model `claude-opus-5-5` unless `-assistant-model` | `ANTHROPIC_API_KEY` | best at tools and the rehearse-first rule |
| `gemini`, `groq`, `openrouter` | **free-tier testing** over the OpenAI-compatible chat API | `ASSISTANT_API_KEY` | needs `-assistant-model` (pick a current free one from the service's list) |
| `ollama` | a model on this machine; nothing leaves it | none | needs `-assistant-model` |
| `openai` | any other compatible service | `ASSISTANT_API_KEY` | needs `-assistant-url` (https) and `-assistant-model` |

```bash
ASSISTANT_API_KEY=... adaetum-agent cluster -enable-assistant \
  -assistant-provider groq -assistant-model <a free model from groq's list> ...
```

The page shows which service and model it is using. Free tiers rate-limit hard (shown
as such), are weaker at choosing tools, and commonly keep and train on what they
receive: **test data only, never real secrets.** Approvals are unchanged because the
conversation loop is the same for every provider (`openai.go` only translates the
wire format), so a weaker model can be less helpful but not less safe. A provider
without tool support will chat but cannot act.

- **It can do what the page can do**, through the same tools MCP offers
  (`internal/clustertools`): cluster, playbooks (list, launch, cancel, read output),
  security switches, join tokens, approving machines, running a command directly,
  plus memory (`remember`, `recall`, `forget`). Each tool is a call to the agent's own
  API, so every rule there applies to it.
- **Reading runs by itself. Anything that changes something stops and asks.** The page
  shows the call with Approve and Deny; nothing happens until you approve, and a call
  nobody answers is denied after 10 minutes.
- **It cannot turn a security switch on.** It tells you which one and why, and you turn
  it on in Security (where the warning and time limit are). It can always turn one off.
  Anything it does to a switch is recorded as made through the assistant, with its reason.
- **Conversations are not stored.** They live in the agent's memory and are dropped
  after a while; what should be kept is saved with `remember`, where it is visible.

### Finding tools (`toolsearch.go`)

A request to the model does not carry every tool, because that costs context in
proportion to the number of tools and the number will grow. It carries the **core**
tools (`clustertools.AssistantCore`, about 2.6 KB of definitions instead of 8.7 KB
for all of them today) and one more, `search_tools`. The model describes what it
needs; the best word matches on name, group and description become available for the
rest of that conversation. The standing instructions name the *groups* that can be
searched, not the tools, so they do not grow as tools are added.

It is done in the agent, not with a provider's own tool-search feature, so it behaves
the same for every provider, including the free OpenAI-compatible ones. Finding a tool
changes only what the model is told: a tool that changes something still stops for
approval, and a tool the model calls without having found it **does not run** (it is
offered, and the model is told to look at it and ask again). Tools from other MCP
servers join the same list with a `Group` of their server's name. Matching is plain
words on purpose; ranking with embeddings would need the embedding service running.

### The same tools through MCP

`adaetum-agent mcp -cluster ... -fingerprint ... -token-file ...` offers the same tool
set to an external assistant (Claude Code, for one). Mutating tools are marked so the
client asks you first; turning security switches **on** additionally needs
`-allow-security-changes`. See [security boundary](security-boundary.md).

### Not built

The assistant sees the cluster agent's surface. Host-level actions that live on a host
agent (removing images, its schedule) are not yet tools; they would need the host's
URL and token. Streaming replies (the page polls), per-person conversations and
identity (there is one shared token), and a cap on spend.
