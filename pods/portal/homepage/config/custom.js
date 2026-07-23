(() => {
  const localPortalHost = "__HOMEPAGE_LOCAL_HOST__".toLowerCase();
  const publicPortalHost = "__HOMEPAGE_PUBLIC_HOST__".toLowerCase();
  const currentHost = window.location.hostname.toLowerCase();

  if (currentHost !== localPortalHost && currentHost !== publicPortalHost) {
    return;
  }

  const useLocalRoutes = currentHost === localPortalHost;
  const routePairs = [
    ["__ARGOCD_PUBLIC_HOST__", "__ARGOCD_LOCAL_HOST__"],
    ["__GITEA_PUBLIC_HOST__", "__GITEA_LOCAL_HOST__"],
    ["__OPENBAO_PUBLIC_HOST__", "__OPENBAO_LOCAL_HOST__"],
    ["__AUTHENTIK_PUBLIC_HOST__", "__AUTHENTIK_LOCAL_HOST__"],
    ["__HEADLAMP_PUBLIC_HOST__", "__HEADLAMP_LOCAL_HOST__"],
    ["__ALERTMANAGER_PUBLIC_HOST__", "__ALERTMANAGER_LOCAL_HOST__"],
    ["__GRAFANA_PUBLIC_HOST__", "__GRAFANA_LOCAL_HOST__"],
    ["__PROMETHEUS_PUBLIC_HOST__", "__PROMETHEUS_LOCAL_HOST__"],
    ["__REGISTRY_PUBLIC_HOST__", "__REGISTRY_LOCAL_HOST__"],
    ["__RANCHER_PUBLIC_HOST__", "__RANCHER_LOCAL_HOST__"],
  ].map(([publicHost, localHost]) => [publicHost.toLowerCase(), localHost.toLowerCase()]);

  const hostMap = new Map(
    routePairs.map(([publicHost, localHost]) => [
      useLocalRoutes ? publicHost : localHost,
      useLocalRoutes ? localHost : publicHost,
    ]),
  );

  const applyRouteScope = () => {
    for (const anchor of document.querySelectorAll("a[href]")) {
      try {
        const url = new URL(anchor.href, window.location.origin);
        const targetHost = hostMap.get(url.hostname.toLowerCase());
        if (targetHost && targetHost !== url.hostname.toLowerCase()) {
          url.hostname = targetHost;
          anchor.href = url.toString();
        }
      } catch {
        // Homepage may briefly render incomplete links while hydrating.
      }
    }
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", applyRouteScope, { once: true });
  } else {
    applyRouteScope();
  }

  new MutationObserver(applyRouteScope).observe(document.documentElement, {
    childList: true,
    subtree: true,
  });
})();
