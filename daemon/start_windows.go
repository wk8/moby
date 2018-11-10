package daemon // import "github.com/docker/docker/daemon"

import (
	"github.com/Microsoft/opengcs/client"
	"github.com/containerd/containerd/oci"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/container"
)

func (daemon *Daemon) getLibcontainerdCreateOptions(container *container.Container) (interface{}, error) {
	// LCOW options.
	if container.OS == "linux" {
		config := &client.Config{}
		if err := config.GenerateDefault(daemon.configStore.GraphOptions); err != nil {
			return nil, err
		}
		// Override from user-supplied options.
		for k, v := range container.HostConfig.StorageOpt {
			switch k {
			case "lcow.kirdpath":
				config.KirdPath = v
			case "lcow.kernel":
				config.KernelFile = v
			case "lcow.initrd":
				config.InitrdFile = v
			case "lcow.vhdx":
				config.Vhdx = v
			case "lcow.bootparameters":
				config.BootParameters = v
			}
		}
		if err := config.Validate(); err != nil {
			return nil, err
		}

		return config, nil
	}

	return nil, nil
}

func getOciSpecOptions(hostConfig *containertypes.HostConfig) []oci.SpecOpts {
	// We don't support any special OCI spec options for windows
	return nil
}
