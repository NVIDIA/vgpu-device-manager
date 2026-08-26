# vgpu-config

A tool to generate a `vGPU Device Manager` configuration file from `vgpuConfig.xml` which ships with the `NVIDIA vGPU Manager`. 
On a live system, `vgpuConfig.xml` gets installed at `/usr/share/nvidia/vgpu/vgpuConfig.xml` and contains a comprehensive list of all vGPU types supported by NVIDIA vGPU.

## Usage

Generate a `config.yaml` file from `vgpuConfig.xml`:

```
vgpu-config generate -f vgpuConfig.xml -o config.yaml
```

For additional help:

```
vgpu-config -h
```
