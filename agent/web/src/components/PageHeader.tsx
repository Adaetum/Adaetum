import { Flex, Heading, Text } from "@radix-ui/themes";
import { createContext, ReactNode, useContext } from "react";

interface PageHeaderProps {
  title: string;
  description?: string;
  actions?: ReactNode;
}

// Inside a Settings tab the page is a section, not the whole screen, so its title steps down.
export const SettingsSection = createContext(false);

export function PageHeader({ title, description, actions }: PageHeaderProps) {
  if (useContext(SettingsSection)) {
    return (
      <Flex justify="between" align="end" gap="4" wrap="wrap" mb="4">
        <Flex direction="column" gap="1">
          <Heading size="4" as="h2">{title}</Heading>
          {description && <Text color="gray" size="2">{description}</Text>}
        </Flex>
        {actions}
      </Flex>
    );
  }
  return (
    <Flex justify="between" align="end" gap="4" wrap="wrap" mb="5">
      <Flex direction="column" gap="1">
        <Heading size="6" as="h1">{title}</Heading>
        {description && <Text color="gray" size="2">{description}</Text>}
      </Flex>
      {actions}
    </Flex>
  );
}
