FROM oven/bun:1.4.2-slim@sha256:cb3bbbb08e13a4a2ff400f24c7a2a1d5efa83f6ef8544d52d95a519631e2fc61
WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile --production
COPY src/ ./src/
ENV NODE_ENV=production
# The socket is mode 0600; the agent and auth sidecars must share this UID.
USER 10001:10001
CMD ["bun", "src/server.ts"]
