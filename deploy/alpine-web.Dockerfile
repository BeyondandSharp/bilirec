# SPDX-License-Identifier: GPL-3.0-or-later
#
# Alpine frontend-only image (nginx:alpine). Exported as
# dist/local-frontend-alpine.tar by build-alpine.sh.

ARG NODE_IMAGE=node:24-alpine
ARG NGINX_IMAGE=nginx:alpine

FROM ${NODE_IMAGE} AS build

ARG PNPM_VERSION=11.1.2
ENV PNPM_HOME=/usr/local/pnpm
ENV PATH=$PNPM_HOME:$PATH

WORKDIR /src
RUN npm install -g pnpm@${PNPM_VERSION}

COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile

COPY web/ ./
RUN pnpm build

FROM ${NGINX_IMAGE} AS runtime

COPY deploy/web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html

EXPOSE 80
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1/ || exit 1

STOPSIGNAL SIGTERM
CMD ["nginx", "-g", "daemon off;"]
