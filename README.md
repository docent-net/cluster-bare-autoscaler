# cluster-bare-autoscaler

# Cluster Bare Autoscaler
## Introduction
Cluster Bare Autoscaler is a tool that automatically adjusts the size of the 
bare-metal Kubernetes cluster when one of the following conditions is true:

- there are nodes in the cluster that have been overutilized for an extended
  period of time
- there are nodes in the cluster that have been underutilized for an extended 
period of time and their pods can be placed on other existing nodes.

This project is similar to the well-known [cluster-autoscaler](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler), 
but with a main difference, that nodes are not terminated nor bootstrapped. 
Instead, nodes are just shutdown, and brought back (like via Wake-on-Lan, IPMI 
or any other, pluggable methods)