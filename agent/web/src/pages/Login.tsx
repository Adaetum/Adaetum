import { Button, Callout, Card, Code, Flex, Heading, Text, TextField } from "@radix-ui/themes";
import { CircleAlert, Info, KeyRound } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";
import { tokenStore } from "../lib/auth";
import { AuthState, fetchAuthState, setUpFirstOwner, signInWithAccount } from "../lib/session";

interface LoginProps {
  // True when a token was already tried and the agent turned it down.
  rejected: boolean;
  onSignedIn: () => void;
}

// Login is the one screen before anything else. It asks the agent what it offers:
//   - accounts, with the first-run setup if nobody has one yet (a code shown on the server);
//   - the access token, always: the emergency way in, and the only way when this agent has
//     no accounts or its sign-in service is down.
// The page itself holds no server data, so showing it before sign-in leaks nothing. The token
// stays in this tab (see lib/auth.ts); an account sign-in is a cookie the agent sets.
export function Login({ rejected, onSignedIn }: LoginProps) {
  const [state, setState] = useState<AuthState | null>(null);
  const [useToken, setUseToken] = useState(false);

  useEffect(() => {
    void fetchAuthState().then(setState);
  }, []);

  const accounts = state?.enabled && !state.unavailable;
  const showToken = !state || !accounts || useToken || (state.mode === "replica" && !state.hasAccounts);

  return (
    <Card size="4" style={{ maxWidth: 480, margin: "10vh auto 0" }}>
      <Flex direction="column" gap="4" py="2">
        <Flex align="center" gap="2">
          <KeyRound size={20} aria-hidden />
          <Heading size="5" as="h1">{state?.needsSetup ? "Set up this server" : "Sign in"}</Heading>
        </Flex>

        {state?.enabled && state.unavailable && (
          <Callout.Root color="amber" size="1" role="status">
            <Callout.Icon><Info size={14} /></Callout.Icon>
            <Callout.Text>Signing in with an account isn't working right now. The access token still does.</Callout.Text>
          </Callout.Root>
        )}
        {state?.enabled && state.mode === "replica" && !state.hasAccounts && (
          <Callout.Root color="amber" size="1" role="status">
            <Callout.Icon><Info size={14} /></Callout.Icon>
            <Callout.Text>This server has not received the accounts from the cluster yet. Use the access token for now.</Callout.Text>
          </Callout.Root>
        )}

        {accounts && state?.needsSetup && <FirstOwner onDone={onSignedIn} />}
        {accounts && !state?.needsSetup && state?.hasAccounts && !useToken && <AccountSignIn onDone={onSignedIn} />}
        {showToken && <TokenSignIn rejected={rejected} onSignedIn={onSignedIn} />}

        {accounts && state?.hasAccounts && !state?.needsSetup && (
          <Button type="button" variant="ghost" color="gray" size="1" onClick={() => setUseToken((value) => !value)}>
            {useToken ? "Sign in with an account instead" : "Use the access token instead"}
          </Button>
        )}
      </Flex>
    </Card>
  );
}

function Problem({ children }: { children: string }) {
  return (
    <Callout.Root color="red" highContrast role="alert">
      <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
      <Callout.Text>{children}</Callout.Text>
    </Callout.Root>
  );
}

function AccountSignIn({ onDone }: { onDone: () => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [problem, setProblem] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setProblem("");
    const result = await signInWithAccount(email, password);
    setBusy(false);
    if (result === "ok") {
      setPassword("");
      onDone();
    } else {
      setProblem(
        result === "wrong" ? "That email and password didn't match." : result === "slow-down" ? "Too many tries. Wait a moment and try again." : "The sign-in service isn't answering. Try the access token instead.",
      );
    }
  };

  return (
    <form onSubmit={submit}>
      <Flex direction="column" gap="3">
        {problem && <Problem>{problem}</Problem>}
        <label>
          <Text as="div" size="2" weight="medium" mb="1">Email</Text>
          <TextField.Root type="email" autoComplete="username" autoFocus value={email} onChange={(event) => setEmail(event.target.value)} />
        </label>
        <label>
          <Text as="div" size="2" weight="medium" mb="1">Password</Text>
          <TextField.Root type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} />
        </label>
        <Button type="submit" disabled={busy || !email.trim() || !password}>{busy ? "Signing in…" : "Sign in"}</Button>
      </Flex>
    </form>
  );
}

// The first person to set up a fresh server must prove they are at it: the code is printed
// on the server's console and kept in a file only the server's administrator can read.
function FirstOwner({ onDone }: { onDone: () => void }) {
  const [form, setForm] = useState({ code: "", name: "", email: "", password: "" });
  const [problem, setProblem] = useState("");
  const [busy, setBusy] = useState(false);
  const set = (key: keyof typeof form) => (event: { target: { value: string } }) => setForm({ ...form, [key]: event.target.value });

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setProblem("");
    const failure = await setUpFirstOwner(form);
    if (failure) {
      setBusy(false);
      setProblem(failure);
      return;
    }
    const signedIn = await signInWithAccount(form.email, form.password);
    setBusy(false);
    if (signedIn === "ok") onDone();
    else setProblem("Your account was made, but signing in failed. Try signing in again.");
  };

  return (
    <form onSubmit={submit}>
      <Flex direction="column" gap="3">
        <Text size="2" color="gray">
          Nobody has an account on this server yet. Make the first one, which will be its owner. You need the setup code shown on the
          server: look in its log, or run <Code variant="ghost">sudo cat /var/lib/adaetum/auth/setup-code</Code>.
        </Text>
        {problem && <Problem>{problem}</Problem>}
        <label>
          <Text as="div" size="2" weight="medium" mb="1">Setup code</Text>
          <TextField.Root autoFocus autoComplete="off" spellCheck={false} placeholder="ABCD-EFGH-JKLM" value={form.code} onChange={set("code")} />
        </label>
        <label>
          <Text as="div" size="2" weight="medium" mb="1">Your name</Text>
          <TextField.Root autoComplete="name" value={form.name} onChange={set("name")} />
        </label>
        <label>
          <Text as="div" size="2" weight="medium" mb="1">Email</Text>
          <TextField.Root type="email" autoComplete="username" value={form.email} onChange={set("email")} />
        </label>
        <label>
          <Text as="div" size="2" weight="medium" mb="1">Password</Text>
          <TextField.Root type="password" autoComplete="new-password" placeholder="At least 12 characters" value={form.password} onChange={set("password")} />
        </label>
        <Button type="submit" disabled={busy || !form.code.trim() || !form.name.trim() || !form.email.trim() || form.password.length < 12}>
          {busy ? "Setting up…" : "Create the owner account"}
        </Button>
      </Flex>
    </form>
  );
}

function TokenSignIn({ rejected, onSignedIn }: { rejected: boolean; onSignedIn: () => void }) {
  const [value, setValue] = useState("");

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const token = value.trim();
    if (!token) return;
    tokenStore.set(token);
    setValue("");
    onSignedIn();
  };

  return (
    <form onSubmit={submit}>
      <Flex direction="column" gap="3">
        <Text color="gray" size="2">
          This server's access token works here even when nothing else does. You can read it on the server with{" "}
          <Code variant="ghost">sudo cat /etc/adaetum/agent-token</Code>.
        </Text>
        {rejected && <Problem>That token wasn't accepted. Check it and try again.</Problem>}
        <label>
          <Text as="div" size="2" weight="medium" mb="1">Access token</Text>
          <TextField.Root type="password" autoComplete="off" spellCheck={false} value={value} onChange={(event) => setValue(event.target.value)} placeholder="Paste the token" />
        </label>
        <Button type="submit" disabled={!value.trim()}>Sign in with the token</Button>
      </Flex>
    </form>
  );
}
