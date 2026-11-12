# syntax=docker/dockerfile:1
# Coolify Dockerfile build pack: one HTTP service on container port 8080.
FROM node:24.22.1-alpine AS frontend
WORKDIR /app/frontend/admin
COPY frontend/admin/package*.json ./
RUN --mount=type=cache,target=/root/.npm \
    --mount=type=secret,id=proxy_ca,required=false \
    if [ -f /run/secrets/proxy_ca ]; then export NODE_EXTRA_CA_CERTS=/run/secrets/proxy_ca; fi; \
    npm ci --strict-ssl=true
COPY frontend/admin/ ./
COPY public/assets/fonts/ /app/public/assets/fonts/
RUN npm run build

FROM golang:1.27.4-alpine AS backend
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=secret,id=proxy_ca,required=false \
    if [ -f /run/secrets/proxy_ca ]; then export SSL_CERT_FILE=/run/secrets/proxy_ca; fi; \
    go mod download
COPY backend/ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=secret,id=proxy_ca,required=false \
    if [ -f /run/secrets/proxy_ca ]; then export SSL_CERT_FILE=/run/secrets/proxy_ca; fi; \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/unibot ./cmd/unibot

FROM scratch
COPY --from=backend /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --chmod=0555 --from=backend /out/unibot /unibot
COPY --from=frontend /app/frontend/admin/dist/ /web/
COPY --chmod=0444 database/seed/university-books.jsonl.gz /database/seed/university-books.jsonl.gz
COPY --chmod=0444 database/seed/directory-safe.jsonl.gz /database/seed/directory-safe.jsonl.gz
COPY --chmod=0444 database/seed/bkatu_contacts_full.json /database/seed/bkatu_contacts_full.json
COPY --chown=65532:65532 --chmod=0700 infra/next-private /files
USER 65532:65532
ENV HTTP_ADDR=0.0.0.0:8080 STATIC_DIR=/web
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 CMD ["/unibot", "-mode", "healthcheck"]
ENTRYPOINT ["/unibot"]
