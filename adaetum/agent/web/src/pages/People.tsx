import { Badge, Button, Callout, Card, Dialog, Flex, Select, Skeleton, Text, TextField } from "@radix-ui/themes";
import { CircleAlert, Info, UserPlus } from "lucide-react";
import { FormEvent, useCallback, useEffect, useState } from "react";
import { PageHeader } from "../components/PageHeader";
import { AuthRequiredError } from "../lib/auth";
import { AuthState, Person, addPerson, fetchAuthState, fetchPeople, removePerson, setPersonRole, signPersonOut, useMe } from "../lib/session";

// People: who can sign in. Everyone signed in can see the list; only an owner changes it,
// and only on the server that owns the accounts (the cluster). A host copies the list from
// the cluster, so it shows it read-only.
export function People() {
  const { me } = useMe();
  const [auth, setAuth] = useState<AuthState | null>(null);
  const [data, setData] = useState<{ mode: string; people: Person[] } | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    try {
      setData(await fetchPeople());
      setError("");
    } catch (failure) {
      setError(failure instanceof AuthRequiredError ? "Sign in to see who has an account." : failure instanceof Error ? failure.message : String(failure));
    }
  }, []);
  useEffect(() => {
    void fetchAuthState().then(setAuth);
    void load();
  }, [load]);

  const owner = Boolean(me && (me.viaToken || me.role === "owner"));
  const replica = data?.mode === "replica";
  const change = async (action: () => Promise<void>, done: string) => {
    setNotice("");
    try {
      await action();
      setNotice(done);
      setError("");
      await load();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    }
  };

  return (
    <>
      <PageHeader title="People" description="Who can sign in to this server with their own account." />
      <Flex direction="column" gap="4">
        {auth && !auth.enabled && (
          <Callout.Root color="gray"><Callout.Icon><Info size={16} /></Callout.Icon><Callout.Text>This server has no accounts set up: it uses the access token only.</Callout.Text></Callout.Root>
        )}
        {error && <Callout.Root color="red" role="alert"><Callout.Icon><CircleAlert size={16} /></Callout.Icon><Callout.Text>{error}</Callout.Text></Callout.Root>}
        {notice && <Callout.Root color="green" role="status" size="1"><Callout.Text>{notice}</Callout.Text></Callout.Root>}
        {replica && (
          <Callout.Root color="gray"><Callout.Icon><Info size={16} /></Callout.Icon>
            <Callout.Text>This server copies its accounts from the cluster, so people are added, changed and removed there. A change reaches this server within a few seconds while the cluster is up.</Callout.Text>
          </Callout.Root>
        )}

        {!data && !error && <Skeleton height="160px" aria-busy="true" aria-label="Loading people" />}
        {data && (
          <Card size="2">
            <div className="advanced-scroll">
              <table className="sizing-table">
                <thead><tr><th>Name</th><th>Email</th><th>Role</th>{owner && !replica && <th aria-label="Actions" />}</tr></thead>
                <tbody>
                  {data.people.map((person) => (
                    <tr key={person.id}>
                      <td>{person.name}{me?.id === person.id && <> <Badge variant="outline" color="gray">you</Badge></>}</td>
                      <td>{person.email}</td>
                      <td>
                        {owner && !replica ? (
                          <Select.Root size="1" value={person.role} onValueChange={(role) => void change(() => setPersonRole(person.id, role), `${person.name} is now ${role === "admin" ? "an admin" : `a${role === "owner" ? "n" : ""} ${role}`}.`)}>
                            <Select.Trigger aria-label={`Role for ${person.name}`} />
                            <Select.Content>
                              <Select.Item value="owner">owner</Select.Item>
                              <Select.Item value="admin">admin</Select.Item>
                              <Select.Item value="member">member</Select.Item>
                            </Select.Content>
                          </Select.Root>
                        ) : person.role}
                      </td>
                      {owner && !replica && (
                        <td>
                          <Flex gap="2" justify="end">
                            <Button size="1" variant="ghost" color="gray" onClick={() => void change(() => signPersonOut(person.id), `${person.name} was signed out everywhere.`)}>Sign out everywhere</Button>
                            <Remove person={person} onConfirm={() => change(() => removePerson(person.id), `${person.name} was removed.`)} />
                          </Flex>
                        </td>
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Card>
        )}

        {owner && data && !replica && <AddPerson onAdd={(person) => change(() => addPerson(person), `${person.name} can now sign in.`)} />}
      </Flex>
    </>
  );
}

function Remove({ person, onConfirm }: { person: Person; onConfirm: () => Promise<void> }) {
  const [open, setOpen] = useState(false);
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger><Button size="1" variant="ghost" color="red">Remove</Button></Dialog.Trigger>
      <Dialog.Content maxWidth="420px">
        <Dialog.Title>Remove {person.name}?</Dialog.Title>
        <Dialog.Description size="2">They will no longer be able to sign in, and will be signed out everywhere. This reaches every server within a few seconds.</Dialog.Description>
        <Flex gap="3" justify="end" mt="4">
          <Dialog.Close><Button variant="soft" color="gray">Keep them</Button></Dialog.Close>
          <Button color="red" onClick={() => void onConfirm().then(() => setOpen(false))}>Remove</Button>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}

// A password the owner can hand over; the new person should change it.
function generate(): string {
  const letters = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  return Array.from(crypto.getRandomValues(new Uint8Array(20)), (n) => letters[n % letters.length]).join("");
}

function AddPerson({ onAdd }: { onAdd: (person: { name: string; email: string; password: string; role: string }) => Promise<void> }) {
  const [form, setForm] = useState({ name: "", email: "", password: "", role: "member" });
  const set = (key: "name" | "email" | "password") => (event: { target: { value: string } }) => setForm({ ...form, [key]: event.target.value });
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    await onAdd(form);
    setForm({ name: "", email: "", password: "", role: "member" });
  };
  return (
    <Card size="3">
      <form onSubmit={submit}>
        <Flex direction="column" gap="3">
          <Flex gap="2" align="center"><UserPlus size={16} aria-hidden /><Text size="3" weight="medium">Add a person</Text></Flex>
          <Flex gap="3" wrap="wrap">
            <TextField.Root style={{ flex: 1, minWidth: 160 }} placeholder="Name" aria-label="Name" value={form.name} onChange={set("name")} />
            <TextField.Root style={{ flex: 1, minWidth: 200 }} type="email" placeholder="Email" aria-label="Email" value={form.email} onChange={set("email")} />
            <Select.Root value={form.role} onValueChange={(role) => setForm({ ...form, role })}>
              <Select.Trigger aria-label="Role" />
              <Select.Content>
                <Select.Item value="member">member (look only)</Select.Item>
                <Select.Item value="admin">admin (can change things)</Select.Item>
                <Select.Item value="owner">owner (also manages people)</Select.Item>
              </Select.Content>
            </Select.Root>
          </Flex>
          <Flex gap="3" wrap="wrap" align="center">
            <TextField.Root style={{ flex: 1, minWidth: 220 }} placeholder="A starting password (12 or more characters)" aria-label="Starting password" value={form.password} onChange={set("password")} autoComplete="off" />
            <Button type="button" variant="soft" color="gray" onClick={() => setForm({ ...form, password: generate() })}>Make one</Button>
            <Button type="submit" disabled={!form.name.trim() || !form.email.trim() || form.password.length < 12}>Add</Button>
          </Flex>
          <Text size="1" color="gray">Give them the password in person or over a private channel, and ask them to change it after signing in. It isn't shown again.</Text>
        </Flex>
      </form>
    </Card>
  );
}
