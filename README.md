# NVIDIA vGPU Device Manager

NVIDIA Virtual GPU (vGPU) enables multiple virtual machines (VMs) to have simultaneous, direct access to a single physical GPU, using the same NVIDIA graphics drivers that are deployed on non-virtualized operating systems.
By doing this, NVIDIA vGPU provides VMs with unparalleled graphics performance, compute performance, and application compatibility, together with the cost-effectiveness and scalability brought about by sharing a GPU among multiple workloads.
Under the control of the NVIDIA Virtual GPU Manager running under the hypervisor, NVIDIA physical GPUs are capable of supporting multiple virtual GPU devices (vGPUs) that can be assigned directly to guest VMs.
To learn more, refer to the [NVIDIA vGPU Software Documentation](https://docs.nvidia.com/grid/).

The `NVIDIA vGPU Device Manager` is a tool designed for system administrators to make working with vGPU devices easier.

It allows administrators to ***declaratively*** define a set of possible vGPU device
configurations they would like applied to all GPUs on a node. At runtime, they
then point `nvidia-vgpu-dm` at one of these configurations, and
`nvidia-vgpu-dm` takes care of applying it. In this way, the same
configuration file can be spread across all nodes in a cluster, and a runtime
flag (or environment variable) can be used to decide which of these
configurations to actually apply to a node at any given time.

As an example, consider the following configuration for a node with two NVIDIA Tesla T4 GPUs.

```
version: v1
vgpu-configs:
  # NVIDIA Tesla T4, Q-Series
  T4-1Q:
    - devices: all
      vgpu-devices:
        "T4-1Q": 16

  T4-2Q:
    - devices: all
      vgpu-devices:
        "T4-2Q": 8

  T4-4Q:
    - devices: all
      vgpu-devices:
        "T4-4Q": 4

  T4-8Q:
    - devices: all
      vgpu-devices:
        "T4-8Q": 2

  T4-16Q:
    - devices: all
      vgpu-devices:
        "T4-16Q": 1

  # Custom configurations
  T4-small:
    - devices: [0]
      vgpu-devices:
        "T4-1Q": 16
    - devices: [1]
      vgpu-devices:
        "T4-2Q": 8

  T4-medium:
    - devices: [0]
      vgpu-devices:
        "T4-4Q": 4
    - devices: [1]
      vgpu-devices:
        "T4-8Q": 2

  T4-large:
    - devices: [0]
      vgpu-devices:
        "T4-8Q": 2
    - devices: [1]
      vgpu-devices:
        "T4-16Q": 1
```

Each of the sections under `vgpu-configs` is user-defined, with custom labels used to refer to them. For example, the `T4-8Q` label refers to the vGPU configuration that creates 2 vGPU devices of type `T4-8Q` on all T4 GPUs on the node. Likewise, the `T4-1Q` label refers to the vGPU configuration that creates 16 vGPU devices of type `T4-1Q` on all T4 GPUs on the node. Finally, the `T4-small` label defines a completely custom configuration which creates 16 `T4-1Q` vGPU devices on the first GPU and 8 `T4-2Q` vGPU devices on the second GPU.

Using the `nvidia-vgpu-dm` tool, the following commands can be run to apply each of these configs in turn:
```
$ nvidia-vgpu-dm apply -f examples/config-t4.yaml -c T4-1Q
$ nvidia-vgpu-dm apply -f examples/config-t4.yaml -c T4-2Q
$ nvidia-vgpu-dm apply -f examples/config-t4.yaml -c T4-4Q
$ nvidia-vgpu-dm apply -f examples/config-t4.yaml -c T4-8Q
$ nvidia-vgpu-dm apply -f examples/config-t4.yaml -c T4-16Q
$ nvidia-vgpu-dm apply -f examples/config-t4.yaml -c T4-small
$ nvidia-vgpu-dm apply -f examples/config-t4.yaml -c T4-medium
$ nvidia-vgpu-dm apply -f examples/config-t4.yaml -c T4-large
```

The currently applied configuration can then be asserted with:
```
$ nvidia-vgpu-dm assert -f examples/config-t4.yaml -c T4-large
INFO[0000] Selected vGPU device configuration is currently applied

$ echo $?
0

$ nvidia-vgpu-dm assert -f examples/config-t4.yaml -c T4-16Q
FATA[0000] Assertion failure: selected configuration not currently applied

$ echo $?
1
```

## Build `nvidia-vgpu-dm`

```
git clone https://gitlab.com/nvidia/cloud-native/vgpu-device-manager.git
cd vgpu-device-manager
make cmd-nvidia-vgpu-dm
```

This will generate a binary called `nvidia-vgpu-dm` in your current directory.

## Usage

#### Prerequisites

- [NVIDIA vGPU Manager](https://docs.nvidia.com/grid/latest/grid-vgpu-user-guide/index.html#installing-configuring-grid-vgpu) is installed on the system.

#### Apply a specific vGPU device config from a configuration file
```
nvidia-vgpu-dm apply -f examples/config-t4.yaml -c T4-1Q
```

#### Apply a specific vGPU device config with debug output
```
nvidia-vgpu-dm -d apply -f examplpes/config-t4.yaml -c T4-1Q
```

#### Apply a one-off vGPU device configuration without a configuration file
```
cat <<EOF | nvidia-vgpu-dm apply -f -
version: v1
vgpu-configs:
  T4-1Q:
  - devices: all
    vgpu-devices:
      "T4-1Q": 16
EOF
```

#### Assert a specific vGPU device configuration is currently applied
```
nvidia-vgpu-dm assert -f examples/config.yaml -c T4-1Q
```

#### Assert a one-off vGPU device configuration without a configuration file
```
cat <<EOF | nvidia-vgpu-dm assert -f -
version: v1
vgpu-configs:
  T4-1Q:
  - devices: all
    vgpu-devices:
      "T4-1Q": 16
EOF
```

#### Assert only that the configuration file is valid and the selected config is present in it
```
nvidia-vgpu-dm assert -f exaples/config.yaml -c T4-1Q --valid-config
```

## Kubernetes Deployment

The [NVIDIA vGPU Device Manager container](https://catalog.ngc.nvidia.com/orgs/nvidia/teams/cloud-native/containers/vgpu-device-manager) manages vGPU devices on a GPU node in a Kubernetes cluster.
The containerized deployment is only supported through the [NVIDIA GPU Operator](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/overview.html).
It is not meant to be run as a standalone component.
The instructions below are for deploying the vGPU Device Manager as a standalone DaemonSet, for development purposes.

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
