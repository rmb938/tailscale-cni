# tailscale-cni
Kubernetes CNI over a tailscale mesh network. https://tailscale.com/

This is a POC and should not be used in production. 

Network Policies are not implemented.

## Requirements

### Tailscale

Install and run `tailscale up` on all Kubernetes Nodes

### Kubernetes Cluster

A Kubernetes cluster deployed with pod-network-cidr set.

Example:
```shell script
kubeadm init --pod-network-cidr=172.18.0.0/16
```

## Install

1. Build and publish the docker image to an image repository
1. Deploy the kustomize manifests while overriding the image to the published image
