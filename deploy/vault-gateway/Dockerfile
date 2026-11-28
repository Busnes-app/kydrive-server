FROM cryptpad/cryptpad@sha256:b1129d954692e9b8a7d198e79d3080d3627051c95137bbf41a7d2683aa53305d AS cryptpad
FROM node:26.13.1-alpine@sha256:6bd9ab43c0ec0190ca7b2d193906cb8b7edf6e7f4afa43ddeccd6452710bffb5
WORKDIR /app
COPY --from=cryptpad /cryptpad/node_modules/ws ./node_modules/ws
COPY deploy/vault-gateway/gateway.mjs deploy/vault-gateway/netflux.mjs ./deploy/vault-gateway/
COPY scripts/vault-relay-authorize.mjs ./scripts/
USER 1000:1000
EXPOSE 9443
ENTRYPOINT ["node", "deploy/vault-gateway/gateway.mjs"]
