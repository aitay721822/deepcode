FROM rust:1.91.1-slim-bookworm@sha256:8514999d4786ef12efe89239e86b3d0a021b94b9d35108c8efe6c79ca7dc1a65
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl jq \
    && curl -fL https://github.com/rust-lang/rust-analyzer/releases/download/2026-11-16/rust-analyzer-x86_64-unknown-linux-gnu.gz -o /tmp/ra.gz \
    && echo '4a08727a03094e5114db4438e4bd1d6182cc26eff37d6962115ea1ec1a491ca3  /tmp/ra.gz' | sha256sum -c - \
    && gzip -d -c /tmp/ra.gz > /usr/local/bin/rust-analyzer && chmod 755 /usr/local/bin/rust-analyzer \
    && rm /tmp/ra.gz && apt-get purge -y --auto-remove curl && rm -rf /var/lib/apt/lists/*
