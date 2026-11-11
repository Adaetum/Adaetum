import { Flex, Text } from "@radix-ui/themes";
import { Check, Circle, X } from "lucide-react";

// The account service accepts passwords of 12 to 128 characters and nothing else
// (adaetum/auth/src/auth.ts, minPasswordLength and maxPasswordLength). These are the
// same numbers, so what the form shows is what the server will enforce.
export const PASSWORD_MIN = 12;
export const PASSWORD_MAX = 128;

export function passwordAcceptable(password: string): boolean {
  return password.length >= PASSWORD_MIN && password.length <= PASSWORD_MAX;
}

type State = "todo" | "met" | "broken";

// PasswordRules lists what a password has to satisfy and ticks each rule off as the person
// types, so nobody has to submit the form to learn what the rules were. A rule they have
// not met yet stays neutral rather than red: it only turns red when they break a limit.
export function PasswordRules({ password }: { password: string }) {
  const length = password.length;
  const rules: { text: string; state: State }[] = [
    {
      text: length > 0 && length < PASSWORD_MIN ? `At least ${PASSWORD_MIN} characters (${length} so far)` : `At least ${PASSWORD_MIN} characters`,
      state: length >= PASSWORD_MIN ? "met" : "todo",
    },
    {
      text: `No more than ${PASSWORD_MAX} characters`,
      state: length > PASSWORD_MAX ? "broken" : length > 0 ? "met" : "todo",
    },
  ];
  return (
    <Flex direction="column" gap="1" mt="2" asChild aria-live="polite">
      <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>
        {rules.map((rule) => (
          <li key={rule.text}>
            <Flex align="center" gap="2">
              {rule.state === "met" ? (
                <Check size={14} color="var(--green-9)" aria-label="met" />
              ) : rule.state === "broken" ? (
                <X size={14} color="var(--red-9)" aria-label="not met" />
              ) : (
                <Circle size={14} color="var(--gray-8)" aria-label="not yet met" />
              )}
              <Text size="1" color={rule.state === "broken" ? "red" : rule.state === "met" ? "green" : "gray"}>
                {rule.text}
              </Text>
            </Flex>
          </li>
        ))}
      </ul>
    </Flex>
  );
}
