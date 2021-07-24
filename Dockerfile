ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:latest

ARG ARCH="amd64"
ARG OS="linux"
COPY bin/tailscale-cni-${OS}-${ARCH} /bin/tailscale-cni
USER root

ENTRYPOINT [ "/bin/tailscale-cni" ]
