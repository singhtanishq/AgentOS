# AgentOS web dashboard — built from the repository root so the pnpm
# workspace (shared packages/) is available.
FROM node:24-alpine AS build
WORKDIR /repo

RUN corepack enable

# Install workspace dependencies first for layer caching.
COPY pnpm-lock.yaml pnpm-workspace.yaml package.json ./
COPY packages/config/package.json packages/config/
COPY apps/web/package.json apps/web/
RUN pnpm install --frozen-lockfile

# Build the application (standalone output).
COPY packages/config packages/config/
COPY apps/web apps/web/
RUN pnpm --filter @agentos/web build

# Run stage uses the self-contained standalone bundle.
FROM node:24-alpine AS run
WORKDIR /app
ENV NODE_ENV=production
ENV PORT=3000
ENV HOSTNAME=0.0.0.0

RUN addgroup -S agentos && adduser -S agentos -G agentos

COPY --from=build /repo/apps/web/.next/standalone ./
COPY --from=build /repo/apps/web/.next/static ./apps/web/.next/static
COPY --from=build /repo/apps/web/public ./apps/web/public

USER agentos
EXPOSE 3000
CMD ["node", "apps/web/server.js"]
