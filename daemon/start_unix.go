// +build !windows

package daemon // import "github.com/docker/docker/daemon"

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/contrib/nvidia"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/runtime/linux/runctypes"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/container"
	"github.com/docker/docker/errdefs"
	"github.com/pkg/errors"
)

func (daemon *Daemon) getRuntimeScript(container *container.Container) (string, error) {
	name := container.HostConfig.Runtime
	rt := daemon.configStore.GetRuntime(name)
	if rt == nil {
		return "", errdefs.InvalidParameter(errors.Errorf("no such runtime '%s'", name))
	}

	if len(rt.Args) > 0 {
		// First check that the target exist, as using it in a script won't
		// give us the right error
		if _, err := exec.LookPath(rt.Path); err != nil {
			return "", translateContainerdStartErr(container.Path, container.SetExitCode, err)
		}
		return filepath.Join(daemon.configStore.Root, "runtimes", name), nil
	}
	return rt.Path, nil
}

// getLibcontainerdCreateOptions callers must hold a lock on the container
func (daemon *Daemon) getLibcontainerdCreateOptions(container *container.Container) (interface{}, error) {
	// Ensure a runtime has been assigned to this container
	if container.HostConfig.Runtime == "" {
		container.HostConfig.Runtime = daemon.configStore.GetDefaultRuntimeName()
		container.CheckpointTo(daemon.containersReplica)
	}

	path, err := daemon.getRuntimeScript(container)
	if err != nil {
		return nil, err
	}
	opts := &runctypes.RuncOptions{
		Runtime: path,
		RuntimeRoot: filepath.Join(daemon.configStore.ExecRoot,
			fmt.Sprintf("runtime-%s", container.HostConfig.Runtime)),
	}

	if UsingSystemd(daemon.configStore) {
		opts.SystemdCgroup = true
	}

	return opts, nil
}

func (daemon *Daemon) getOciSpecOptions(container *container.Container) []oci.SpecOpts {
	hostGpuConfig := container.HostConfig.GpuConfig
	labelGpuConfig := parseGPUConfigOptions(container.Config.Labels)
	gpuConfig := daemon.mergeGpuConfigOptions(hostGpuConfig, labelGpuConfig)

	var deviceOpt nvidia.Opts

	if gpuConfig.All {
		deviceOpt = nvidia.WithAllDevices
	} else if len(gpuConfig.DeviceUUIDs) != 0 {
		deviceOpt = nvidia.WithDeviceUUIDs(gpuConfig.DeviceUUIDs...)
	} else if len(gpuConfig.Devices) != 0 {
		deviceOpt = nvidia.WithDevices(gpuConfig.Devices...)
	}

	if deviceOpt != nil {
		// TODO (jrouge): do we want to surface the capabilities to the API too?
		return []oci.SpecOpts{nvidia.WithGPUs(deviceOpt, nvidia.WithAllCapabilities)}
	}

	return nil
}

func parseGPUConfigOptions(labels map[string]string) containertypes.GpuConfig {
	var gpuConfig containertypes.GpuConfig
	gpuDevice, ok := labels["annotation.deviceType"]
	if !ok || gpuDevice != "nvidia.com/gpu" {
		return gpuConfig
	}
	allDevices, ok := labels["annotation.allDevicesVisible"]
	if ok && allDevices == "true" {
		gpuConfig.All = true
		return gpuConfig
	}
	visibleDevices, ok := labels["annotation.visibleDevices"]
	if !ok {
		return gpuConfig
	}
	gpuConfig.DeviceUUIDs = strings.Split(visibleDevices, ",")
	return gpuConfig
}

// Merge gpu options from the host config and from the label config. The host options take precedence.
func (daemon *Daemon) mergeGpuConfigOptions(hostOptions, labelOptions containertypes.GpuConfig) containertypes.GpuConfig {
	if !areGpuOptionsEmpty(hostOptions) {
		return hostOptions
	}

	if !areGpuOptionsEmpty(labelOptions) {
		return hostOptions
	}

	return containertypes.GpuConfig{All: daemon.configStore.GpuEnabled}
}

func areGpuOptionsEmpty(opts containertypes.GpuConfig) bool {
	return !opts.All && len(opts.DeviceUUIDs) == 0 && len(opts.Devices) == 0
}
