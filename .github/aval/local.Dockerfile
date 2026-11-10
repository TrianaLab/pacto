# The linux/amd64 image `make aval-local` (.github/aval/local.sh) runs suites
# in, so a maintainer's run on any machine matches the platform and the exact
# tool versions the contracts declare: Go 1.27.3 (the base image, the pin of
# integrations/kubernetes/Dockerfile), Node 22.23.4, cosign 3.1.4 and jq 1.7.1,
# each checked against a pinned sha256, and PyYAML and jsonschema for the docs
# gates (CI's runner image ships them). Bump them with the contracts and ci.yml.
FROM golang:1.27.3@sha256:e519d3b0a5b285e5aa53ab9eef02bc6bbb9cb04608389b743a136a9ce75f0d1e
ARG NODE_VERSION=22.23.4
ARG NODE_SHA256=b8492284c76a54baf58d61668dd0e41ee0fc98adabf9503d30f05d08f55b4406
ARG COSIGN_VERSION=3.1.4
ARG COSIGN_SHA256=424e2c97d0b45fd7a6b608a87f090f35633f4c23ece5f092d48bba11aaf4bbfa
ARG JQ_VERSION=1.7.1
ARG JQ_SHA256=5942c9b0934e510ee61eb3e30273f1b3fe2590df93933a93d7c58b81d19c8ff5
RUN apt-get update && apt-get install -y --no-install-recommends python3-yaml=6.0.2-1+b2 python3-jsonschema=4.19.2-6 && rm -rf /var/lib/apt/lists/* \
 && curl -fsSL -o /usr/local/bin/jq "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-amd64" \
 && echo "${JQ_SHA256}  /usr/local/bin/jq" | sha256sum -c - && chmod +x /usr/local/bin/jq \
 && curl -fsSL -o /tmp/node.tgz "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.gz" \
 && echo "${NODE_SHA256}  /tmp/node.tgz" | sha256sum -c - \
 && tar -xzf /tmp/node.tgz -C /usr/local --strip-components=1 && rm /tmp/node.tgz \
 && curl -fsSL -o /usr/local/bin/cosign "https://github.com/sigstore/cosign/releases/download/v${COSIGN_VERSION}/cosign-linux-amd64" \
 && echo "${COSIGN_SHA256}  /usr/local/bin/cosign" | sha256sum -c - && chmod +x /usr/local/bin/cosign \
 && git config --system safe.directory '*'
