# Dolos (MIT, Ghent University) with a small HTTP wrapper. The service has no network access in compose (internal network).
# Dolos v2.10.0, pinned by digest.
FROM ghcr.io/dodona-edu/dolos-cli@sha256:64ca9827208a4893975a9a2adcc22a8f16c53341f773f4139c1410cf948930af
COPY server.mjs /srv/server.mjs
USER 1000:1000
ENTRYPOINT ["node", "/srv/server.mjs"]
