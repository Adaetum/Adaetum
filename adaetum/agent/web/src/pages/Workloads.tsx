import { Flex, Text, TextField } from "@radix-ui/themes";
import { Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Report } from "../api";
import { AppCards } from "../components/AppCards";
import { APP_TABS, AppPage, AppTab, MissingApp, appHref } from "../components/AppPage";
import { Advanced, AdvancedBlock } from "../components/Advanced";
import { Toggle } from "../components/Filters";
import { PageHeader } from "../components/PageHeader";
import { AppLocation } from "../app/routes";
import { cardKey, summarizeAppCards } from "../lib/apps";
import { useHistory } from "../lib/historyApi";
import { sizingFor } from "../lib/sizing";
import { ClusterAddresses, ClusterCertificates } from "../components/ClusterAccess";
import { ClusterDeployments } from "../components/ClusterDeployments";
import { ClusterStorage } from "../components/ClusterStorage";
import { ClusterWarnings } from "../components/ClusterWarnings";

// The simplified view of everything that runs: one card per APP. What is running, how it
// is set up, how full its storage is and whether it is up to date are all facets of the
// app, so they live on its card and in its dialog rather than on separate pages for
// components, instances, volumes and deployments. What is about the whole cluster rather than
// one app (disk capacity, image cleanup, every deployment) sits under Advanced.
export function Workloads({ report, location, refresh }: { report: Report; location: AppLocation; refresh: () => void }) {
  const [search, setSearch] = useState("");
  const [problemsOnly, setProblemsOnly] = useState(location.params.get("problems") === "1");
  const [showSystem, setShowSystem] = useState(false);
  const history = useHistory();

  const autoscalers = report.rke2.autoscalers ?? [];
  const cards = useMemo(
    () => summarizeAppCards(report.rke2.workloads ?? [], report.rke2.pods ?? [], report.rke2.argoApps ?? [], report.rke2.volumes ?? []),
    [report.rke2.workloads, report.rke2.pods, report.rke2.argoApps, report.rke2.volumes],
  );

  // A link from an alert ("focus") opens the cluster-wide section it is about and scrolls to it.
  const focus = location.params.get("focus");
  // The section above the target (storage) fills in as data arrives, so scroll again once it has.
  useEffect(() => {
    if (!focus || location.params.get("app")) return;
    const scroll = () => document.getElementById(`cluster-${focus}`)?.scrollIntoView({ block: "start" });
    scroll();
    const later = window.setTimeout(scroll, 500);
    return () => window.clearTimeout(later);
  }, [focus, location.params]);

  // An app in the address bar means "show that app's own page".
  const appId = location.params.get("app");
  const tabParam = location.params.get("tab");
  const tab: AppTab = APP_TABS.find((item) => item.id === tabParam)?.id ?? "overview";
  const open = appId ? cards.find((card) => cardKey(card) === appId) : undefined;

  const needle = search.trim().toLowerCase();
  const visible = useMemo(() => {
    const scored = cards
      // The platform's own apps stay out of the way, unless one needs attention.
      .filter((card) => showSystem || !card.system || !card.healthy)
      .filter((card) => (!problemsOnly || !card.healthy) && (!needle || card.name.toLowerCase().includes(needle)))
      .map((card) => ({ card, score: sizingFor(card, autoscalers, history)?.points ?? 101 }));
    // Broken first, then the worst set-up first: the top of the page is where to look.
    return scored.sort((a, b) => Number(a.card.healthy) - Number(b.card.healthy) || a.score - b.score || a.card.name.localeCompare(b.card.name)).map((item) => item.card);
  }, [cards, showSystem, problemsOnly, needle, autoscalers, history]);

  if (appId) {
    return open ? (
      <AppPage card={open} cards={cards} report={report} sizing={sizingFor(open, autoscalers, history)} tab={tab}
        onTab={(next) => { window.location.hash = appHref(open, next); }} />
    ) : (
      <MissingApp id={appId} />
    );
  }

  return (
    <>
      <PageHeader title="Workloads" description="Everything you run, one card per app: how it is doing, how well it is set up, and how full its storage is." />
      <Flex gap="3" align="center" wrap="wrap" mb="3">
        <TextField.Root value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search apps" aria-label="Search apps" style={{ minWidth: 220 }}>
          <TextField.Slot><Search size={16} aria-hidden /></TextField.Slot>
        </TextField.Root>
        <Toggle label="Problems only" checked={problemsOnly} onChange={setProblemsOnly} />
        <Toggle label="Show system apps" checked={showSystem} onChange={setShowSystem} />
        <Text size="1" color="gray">Worst set-up first.</Text>
      </Flex>
      <AppCards
        apps={visible}
        autoscalers={autoscalers}
        history={history}
        hrefOf={(card) => appHref(card)}
        empty={problemsOnly ? "Every app is running normally." : needle ? "No app matches that search." : "No apps were reported."}
      />
      <div>
        <Advanced open={Boolean(focus)}>
          <div id="cluster-warnings" />
          <AdvancedBlock title="Recent warnings" note="Problems the cluster has reported recently, newest first, including ones about servers and storage that belong to no app. An app's own are on its Overview tab.">
            <ClusterWarnings report={report} />
          </AdvancedBlock>
          <div id="cluster-certificates" />
          <AdvancedBlock title="Security certificates" note="Certificates are the cluster's, not one app's, so the ones about to expire are listed here.">
            <ClusterCertificates report={report} />
          </AdvancedBlock>
          <div id="cluster-addresses" />
          <AdvancedBlock title="Web addresses" note="Every address that reaches the cluster, including ones that belong to no app. An app's own are on its “How it fits” tab.">
            <ClusterAddresses report={report} />
          </AdvancedBlock>
          <div id="cluster-storage" />
          <AdvancedBlock title="Cluster storage" note="Disk capacity and image cleanup are about the whole cluster, not one app. Each app's own volumes are on its Storage tab.">
            <ClusterStorage report={report} refresh={refresh} />
          </AdvancedBlock>
          <div id="cluster-deployments" />
          <AdvancedBlock title="Every deployment" note="Each part of the platform is kept in line with the configuration in your recovery repository. “Out of date” means what is running differs from it. This includes deployments that belong to no app; an app's own are on its Deployments tab.">
            <ClusterDeployments report={report} initialAttention={focus === "deployments"} />
          </AdvancedBlock>
        </Advanced>
      </div>
    </>
  );
}
