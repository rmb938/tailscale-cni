FROM alpine:3.14

RUN apk add --no-cache \
    iptables \
    iproute2

ARG ARCH="amd64"
ARG OS="linux"
COPY bin/tailscale-cni-${OS}-${ARCH} /bin/tailscale-cni
USER root

ENTRYPOINT [ "/bin/tailscale-cni" ]
