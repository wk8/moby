// +build !windows

package daemon // import "github.com/docker/docker/daemon"

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"strconv"
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

func (daemon *Daemon) getOciSpecOptions(cont *container.Container) []oci.SpecOpts {
	hostGpuConfig := cont.HostConfig.GpuConfig
	labelGpuConfig := parsGPUConfigOptions(cont.Config.Labels)
	gpuConfig := daemon.mergeGpuConfigOptions(hostGpuConfig, labelGpuConfig)

	var deviceOpt nvidia.Opts

	if gpuConfig.All {
		deviceOpt = nvidia.WithAllDevices
	} else if len(gpuConfig.Devices) != 0 {
		deviceOpt = nvidia.WithDevices(gpuConfig.Devices...)
	}

	if deviceOpt != nil {
		// TODO (jrouge): do we want to surface the capabilities to the API too?
		return []oci.SpecOpts{nvidia.WithGPUs(deviceOpt, nvidia.WithAllCapabilities)}
	}

	return nil
}

func parsGPUConfigOptions(labels map[string]string) containertypes.GpuConfig {
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
	gpuConfig.Devices = splitInto(visibleDevices)
	return gpuConfig
}

func splitInto(val string) []int {
	var result []int
	if !strings.Contains(val, ",") {
		num, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			return result
		}
		result = append(result, num)
		return result
	}
	toks := strings.Split(val, ",")
	for _, c := range toks {
		num, err := strconv.Atoi(strings.TrimSpace(c))
		if err != nil {
			continue
		}
		result = append(result, num)
	}
	return result
}

// Merge gpu options from the host config and from the label config. The host options take precedence.
func (daemon *Daemon) mergeGpuConfigOptions(hostOptions, labelOptions containertypes.GpuConfig) containertypes.GpuConfig {
	var mergedOptions containertypes.GpuConfig
	if hostOptions.All {
		mergedOptions.All = true
	} else {
		mergedOptions.All = labelOptions.All
	}
	if len(hostOptions.Devices) != 0 {
		mergedOptions.Devices = hostOptions.Devices
	} else {
		mergedOptions.Devices = mergedOptions.Devices
	}
	if daemon.configStore.GpuEnabled && len(mergedOptions.Devices) == 0 {
		mergedOptions.All = true
	}
	return mergedOptions
}
