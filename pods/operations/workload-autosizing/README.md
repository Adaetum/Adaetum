# Workload autosizing

Adaetum splits automatic resource management into deliberately narrow owners:

- Horizontal Pod Autoscalers own replica counts and scale on CPU utilization.
  Every managed workload retains at least two replicas, scales up immediately,
  and waits 15 minutes before removing capacity.
- Vertical Pod Autoscaler owns only the main container's memory request. It uses
  Kubernetes in-place resize, may not change limits or CPU, and will wait rather
  than evict a pod when a safe live resize is unavailable.
- Descheduler may rebalance only pods labeled
  `autoscaling.adaetum.io/rebalance=true`. Those workloads also have two or more
  replicas and a PodDisruptionBudget. Each run has strict eviction caps.
- Goldilocks is a read-only view of the VPA recommendations. Its controller is
  disabled so Git remains the only policy source of truth.

The initial allowlist is Cloudflared, Apprise, and Homepage. Stateful workloads,
singletons, storage services, node agents, bootstrap services, and cluster
control-plane components remain excluded until their own availability contract
proves that automatic scaling and voluntary eviction are safe.

Review Goldilocks and Prometheus history before changing the Git baselines or
VPA bounds. A recommendation is evidence, not permission to remove the floors,
limits, disruption budgets, or the separation between HPA CPU and VPA memory.
