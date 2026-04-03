(() => {
  const applyHomepageScopeFilter = () => {
  const host = window.location.hostname.toLowerCase();
  const localView = host.endsWith(".local");
  const publicView = host.endsWith(".cloud");

  if (!localView && !publicView) {
    return;
  }

  const cardAnchors = Array.from(document.querySelectorAll('a[href]')).filter((anchor) => {
    try {
      const hrefHost = new URL(anchor.href, window.location.origin).hostname.toLowerCase();
      return hrefHost.endsWith(".local") || hrefHost.endsWith(".cloud");
    } catch {
      return false;
    }
  });

  const normalizeCardText = (card, sourceSuffix) => {
    const walker = document.createTreeWalker(card, NodeFilter.SHOW_TEXT);
    const suffixPattern = sourceSuffix === "local" ? /\s+\(internal\)/gi : /\s+\(public\)/gi;
    while (walker.nextNode()) {
      const node = walker.currentNode;
      node.textContent = node.textContent.replace(suffixPattern, "");
    }
  };

  const findCardContainer = (anchor) => {
    const candidates = [];
    let node = anchor;
    while (node && node !== document.body) {
      if (
        node.matches?.(
          '.service, .service-card, .card, li, article, section, [class*="service"], [class*="card"]',
        )
      ) {
        candidates.push(node);
      }
      node = node.parentElement;
    }

    if (candidates.length === 0) {
      return anchor.parentElement;
    }

    return candidates[candidates.length - 1];
  };

  for (const anchor of cardAnchors) {
    const hrefHost = new URL(anchor.href, window.location.origin).hostname.toLowerCase();
    const targetScope = hrefHost.endsWith(".local") ? "local" : "public";
    const card = findCardContainer(anchor);
    if (!card) {
      continue;
    }
    if ((localView && targetScope === "public") || (publicView && targetScope === "local")) {
      card.style.display = "none";
      continue;
    }
    normalizeCardText(card, targetScope);
  }
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", applyHomepageScopeFilter, { once: true });
  } else {
    applyHomepageScopeFilter();
  }

  const observer = new MutationObserver(() => applyHomepageScopeFilter());
  observer.observe(document.documentElement, { childList: true, subtree: true });

  window.addEventListener("load", applyHomepageScopeFilter, { once: true });
  setTimeout(applyHomepageScopeFilter, 500);
  setTimeout(applyHomepageScopeFilter, 1500);
})();
