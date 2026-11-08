import { Flex, Select, Switch, Text } from "@radix-ui/themes";

// Radix Select forbids an empty item value, so "all" needs a sentinel.
const ALL = "__all__";

interface AppSelectProps {
  apps: string[];
  value: string;
  onChange: (app: string) => void;
}

// Apps are namespaces; the UI never says "namespace".
export function AppSelect({ apps, value, onChange }: AppSelectProps) {
  return (
    <Select.Root value={value || ALL} onValueChange={(next) => onChange(next === ALL ? "" : next)}>
      <Select.Trigger aria-label="App" style={{ minWidth: 180 }} />
      <Select.Content>
        <Select.Item value={ALL}>All apps</Select.Item>
        {apps.map((app) => (
          <Select.Item key={app} value={app}>
            {app}
          </Select.Item>
        ))}
      </Select.Content>
    </Select.Root>
  );
}

interface ToggleProps {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}

export function Toggle({ label, checked, onChange }: ToggleProps) {
  return (
    <Text as="label" size="2">
      <Flex gap="2" align="center">
        <Switch checked={checked} onCheckedChange={onChange} size="1" />
        {label}
      </Flex>
    </Text>
  );
}
