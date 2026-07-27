# Crafty Controller

## What it does

Crafty manages a Minecraft Bedrock server as a traditional, fixed-size stateful
workload. The deployment runs one `Recreate` replica with explicit resources and
a 50 Gi Longhorn `ReadWriteOnce` volume. HPA, VPA, and the descheduler do not own
this workload because scaling or moving a live game server is not interchangeable
with scaling a stateless web application.

The management panel and game traffic intentionally use different routes:

- `https://crafty.<public-domain>` reaches the panel through nginx, Cloudflare,
  and Authentik.
- `https://crafty.<public-domain>.local` reaches the panel through trusted local
  DNS and nginx without Authentik forward-auth.
- `minecraft.<local-domain>:19132` reaches Bedrock through the UDP stream
  listener on the same ingress-nginx LoadBalancer and LAN VIP.
- `minecraft.<tailnet>.ts.net:19132` reaches the Bedrock server over UDP through
  the Tailscale Operator shared ProxyGroup.

Only UDP/19132 is exposed for gameplay. The Java server range and optional
Dynmap port are not published by this example.

## First login and Bedrock setup

Crafty generates the initial `admin` credential in its persistent configuration
directory. After the deployment starts, retrieve it with:

```bash
kubectl -n games exec deploy/crafty -- cat /crafty/app/config/default-creds.txt
```

Sign in through either panel route and change that password immediately. Create
the Bedrock server with port `19132`. LAN clients use
`minecraft.<local-domain>:19132`; Tailscale-connected clients use the
`minecraft` MagicDNS name on the same port.

## Defaults

- Crafty image: `4.10.7`, pinned by digest
- Replicas: `1`, using `Recreate`
- Requests: `1` CPU and `4 Gi` memory
- Limits: `4` CPU and `8 Gi` memory
- Persistent storage: `50 Gi`, Longhorn `ReadWriteOnce`
- Panel: HTTPS/8443, exposed only through public and local ingress
- Local Bedrock: UDP/19132 through the ingress-nginx LAN VIP
- Tailnet Bedrock: UDP/19132 through the Tailscale Operator
