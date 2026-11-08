// Presentation copy only. Stage order and execution remain owned by the Go engine.
export const chapterNumerals = ["I", "II", "III", "IV", "V", "VI", "VII", "VIII"];

export const journey: Record<string, { title: string; purpose: string; outcome: string }> = {
  repository: { title: "Secure your recovery base", purpose: "Create a private GitHub home for your cluster so you can rebuild it later.", outcome: "A private recovery repository connected to this checkout, with Adaetum preserved as upstream." },
  providers: { title: "Gather service access", purpose: "Connect Cloudflare and Tailscale so your cluster can reach its domain, storage and private network.", outcome: "Your Cloudflare zone and Tailscale tailnet selected, with scoped access prepared for setup." },
  profile: { title: "Choose your realm", purpose: "Set the domain and private network your cluster will use, then review the public settings before they are published.", outcome: "Your platform.yaml and derived configuration published to your private repository." },
  media: { title: "Choose your install image", purpose: "Select and verify the Rocky Linux image for your first server.", outcome: "A local ISO verified against Rocky’s published checksum." },
  validate: { title: "Check your loadout", purpose: "Validate your profile and confirm provider access before building the installer.", outcome: "Read-only checks completed, or a clear explanation of what needs attention." },
  render: { title: "Forge cluster configuration", purpose: "Turn your reviewed settings into the runtime and GitOps files your cluster needs to start.", outcome: "Runtime configuration generated and tracked configuration committed." },
  artifacts: { title: "Build and deploy your kit", purpose: "Package your installer, publish recovery assets and start the repository workflows.", outcome: "Artifacts sent to R2, secrets synchronized to GitHub and workflows triggered." },
  finalize: { title: "Launch your first server", purpose: "Save the verified installer ISO and see what happens when you boot it.", outcome: "A verified installer ISO ready to attach to your server or virtual machine." },
};
