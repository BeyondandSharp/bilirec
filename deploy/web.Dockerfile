# syntax=docker/dockerfile:1.7
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Static build of the frontend (web/, the React PWA formerly bilirec-web/)
# served by nginx.
# The frontend is a thin client: open it, fill in the backend address
# (e.g. http://<host>:8080) on the login screen, and it talks to the API.
#
#   docker build -f deploy/web.Dockerfile -t bilirec-web:dev .
#   docker compose -f deploy/docker-compose.yml up --build   # starts both

FROM node:24-slim AS build

ARG PNPM_VERSION=11.1.2
ENV PNPM_HOME=/usr/local/pnpm
ENV PATH=$PNPM_HOME:$PATH

WORKDIR /src
RUN npm install -g pnpm@${PNPM_VERSION}

# Dependency layer first for caching.
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile

COPY web/ ./
RUN pnpm build

FROM nginx:1.27-alpine AS runtime

COPY deploy/web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html

EXPOSE 80
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1/ || exit 1

STOPSIGNAL SIGTERM
CMD ["nginx", "-g", "daemon off;"]
