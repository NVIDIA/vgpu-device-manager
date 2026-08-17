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

## Supported vGPU frameworks

The NVIDIA vGPU Manager driver exposes vGPU devices through one of two frameworks, depending on the GPU architecture:

- **Mediated devices (mdev)**: used on GPUs up to and including the Ampere architecture. vGPU devices are created through `/sys/class/mdev_bus`.
- **Vendor-specific VFIO**: used by vGPU 17.0+ on the Ada, Hopper and newer architectures (e.g. L40S, H100, H200). There is no mdev bus on these systems. Each GPU is an SR-IOV physical function bound to the `nvidia` driver, and vGPU devices are created on its virtual functions by writing a vGPU type ID to the per-VF `nvidia/current_vgpu_type` sysfs file. See the [NVIDIA vGPU documentation](https://docs.nvidia.com/vgpu/latest/grid-vgpu-user-guide/index.html) for details.

`nvidia-vgpu-dm` detects the framework automatically and uses the same configuration file format and CLI semantics for both. The detection is node-wide: a single framework is selected for all GPUs on the node, so nodes mixing GPUs from both framework generations are not supported.

On systems using the vendor-specific VFIO framework:

- SR-IOV virtual functions are enabled automatically (via the `sriov-manage` script shipped with the vGPU Manager driver) if they are not enabled already. This requires the vGPU Manager driver to be installed on the host root filesystem; with a containerized driver the virtual functions are expected to be enabled by the driver container itself. When `nvidia-vgpu-dm apply` runs inside a container, pass `--host-root-mount` (environment variable `VGPU_DM_HOST_ROOT_MOUNT`) so that `sriov-manage` is run from the host driver installation through chroot.
- MIG-backed vGPU types require MIG mode to be enabled and GPU instances to be created before the vGPU devices can be created. The Kubernetes deployment handles this automatically; standalone CLI users must configure MIG first (e.g. with [mig-parted](https://github.com/NVIDIA/mig-parted)).
- NVML (`libnvidia-ml.so.1`) is used to resolve the vGPU type names of already-created vGPU devices, since the sysfs `creatable_vgpu_types` files only list types that can still be created. When deploying in Kubernetes, use a deployment that mounts the driver installation and sets `LD_PRELOAD` (see `examples/nvidia-vgpu-device-manager-mig-support-example.yaml`).
- MIG mode persists across reboots, but MIG instances, SR-IOV virtual functions and vGPU devices do not; they are re-created on every boot, which is exactly what the Kubernetes deployment does when it applies the selected configuration at node startup.
- Applying a configuration at boot (before VMs are started) is the reliable path. Live re-configuration after MIG changes is best-effort: on some driver versions the `creatable_vgpu_types` enumeration has been observed to come up empty after MIG instances are re-created without a reboot (restarting the host `nvidia-vgpud` service may help).

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
