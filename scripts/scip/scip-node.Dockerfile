FROM node:24.11.1-bookworm-slim@sha256:48abc13a19400ca3985071e287bd405a1d99306770eb81d61202fb6b65cf0b57
RUN npm install -g @sourcegraph/scip-typescript@0.6.2 @sourcegraph/scip-python@0.6.8
