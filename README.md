# NVIDIA vGPU Device Manager

The `NVIDIA vGPU Device Manager` manages vGPU devices on a GPU node in a Kubernetes cluster.
It defines a schema for declaratively specifying the list of vGPU types one would like to create on the node.
The vGPU Device Manager parses this schema and applies the desired config by creating vGPU devices following steps outlined in the
[NVIDIA vGPU User Guide](https://docs.nvidia.com/grid/latest/grid-vgpu-user-guide/index.html#creating-vgpu-device-red-hat-el-kvm).

As an example, consider the following configuration for a node with NVIDIA A100 PCIe 40GB cards.

```
version: v1
vgpu-configs:
  default:
    - "A100-40C"

  # NVIDIA A100 PCIe 40GB, C-Series
  A100-40C:
    - "A100-40C"
  A100-20C:
    - "A100-20C"
  A100-10C:
    - "A100-10C"
  A100-8C:
    - "A100-8C"
  A100-5C:
    - "A100-5C"
  A100-4C:
    - "A100-4C"

  # Custom configurations
  A100-small:
    - "A100-4C"
    - "A100-5C"
  A100-medium:
    - "A100-8C"
    - "A100-10C"
  A100-large:
    - "A100-20C"
    - "A100-40C"
```

Each of the sections under `vgpu-configs` is user-defined, with custom labels used to refer to them. For example, the `A100-20C` label refers to the vGPU configuration that creates vGPU devices of type `A100-20C` on all GPUs on the node. Likewise, the `A100-4C` label refers to the vGPU configuration that creates vGPU devices of type `A100-4C` on all GPUs on the node.

More than one vGPU type can be associated with a configuration. For example, the `A100-small` label specifies both the `A100-4C` and `A100-5C` vGPU types. If the node has multiple A100 cards, then vGPU devices of both types will be created on the node. More specifically, the vGPU Device Manager will select the vGPU types in a round robin fashion as it creates devices. vGPU devices of type `A100-4C` get created on the first card, vGPU devices of type `A100-5C` get created on the second card, vGPU devices of type `A100-4C` get created on the third card, etc.

## Prerequisites

- [NVIDIA vGPU Manager](https://docs.nvidia.com/grid/latest/grid-vgpu-user-guide/index.html#installing-configuring-grid-vgpu) is installed on the system.

## Usage

**Note:** Currently this project can only be deployed on Kubernetes, and the only supported way is through the [NVIDIA GPU Operator](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/overview.html). It is not meant to be run as a standalone component and no CLI utility exists. The instructions below are for deploying the vGPU Device Manager as a standalone DaemonSet, for development purposes.

First, create a vGPU devices configuration file. The example file in `examples/` can be used as a starting point:

```
wget -O config.yaml https://raw.githubusercontent.com/NVIDIA/vgpu-device-manager/main/examples/config-example.yaml
```

Modify `config.yaml` as needed. Then, create a ConfigMap for it:

```
kubectl create configmap vgpu-devices-config --from-file=config.yaml
```

Deploy the vGPU Device Manager:

```
kubectl apply -f https://raw.githubusercontent.com/NVIDIA/vgpu-device-manager/main/examples/nvidia-vgpu-device-manager-example.yaml
```

The example DaemonSet will apply the `default` vGPU configuration by default. To override and pick a new configuration, label the worker node `nvidia.com/vgpu.config=<config>`, where `<config>` is the name of a valid configuration in `config.yaml`. The vGPU Device Manager continuously watches for changes to this label.
