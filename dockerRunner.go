package daprdockr

import (
	"github.com/coreos/go-etcd/etcd"
	"github.com/dotcloud/docker"
	dockerclient "github.com/fsouza/go-dockerclient"
	"log"
	"os"
	"strconv"
	"strings"
)

const (
	ContainerStopTimeout = 30 // seconds
)

// Pull required state changes from the store and attempt to apply them locally.
func ApplyRequiredStateChanges(dockerClient *dockerclient.Client, etcdClient *etcd.Client, requiredChanges chan map[string]*RequiredStateChange, stop chan bool) {
	for requiredChange := range requiredChanges {
		for _, change := range requiredChange {
			instanceName := change.ServiceConfig.InstanceQualifiedName(change.Instance)
			switch change.Operation {
			case Add:
				/*err := <-prepareForService(dockerClient, change.ServiceConfig)
				if err != nil {
					if errors != nil {
						*errors <- err
					}
					continue
				}*/

				if err := LockInstance(etcdClient, change.Instance, change.ServiceConfig); err == nil {
					log.Printf("[DockerRunner] Acquired lock on instance %s\n", change.ServiceConfig.InstanceQualifiedName(change.Instance))
					err = instantiateService(dockerClient, change.ServiceConfig, change.Instance)
					if err != nil {
						log.Printf("[DockerRunner] Failed to instantiate %s: %s.\n", instanceName, err)
					} else {
						log.Printf("[DockerRunner] Instantiated %s.\n", instanceName)
					}
				} else {
					log.Printf("[DockerRunner] Could not acquire lock: %s.\n", err)
				}
			case Remove:
				log.Printf("[DockerRunner] Attempting to remove instance %s.\n", instanceName)
				err := removeContainer(dockerClient, change.ServiceConfig, change.Instance)
				if err != nil {
					log.Printf("[DockerRunner] Failed to remove instance %s. Instance might not exist locally. %s\n", instanceName, err)
				} else {
					log.Printf("[DockerRunner] Removed instance %s.\n", instanceName)
				}
			}
		}
	}
	log.Printf("[DockerRunner] Exiting.\n")
}

// Prepares for a service to be instantiated by pulling the container's image.
func prepareForService(client *dockerclient.Client, config *ServiceConfig) (err error) {
	//TODO: Account for different registries.
	imageOpts := dockerclient.PullImageOptions{Repository: config.Container.Image}
	images, err := client.ListImages(true)
	if err != nil {
		return
	}

	shouldPullImage := true
	for _, image := range images {
		// Check for a prefix match.
		if strings.HasPrefix(image.ID, config.Container.Image) {
			shouldPullImage = false
		}

		if !shouldPullImage {
			break
		}

		// Check each tag to determine whether it might be satisfy the image requirement.
		if !strings.Contains(config.Container.Image, ":") {
			for _, tag := range image.RepoTags {
				tag = strings.Split(tag, ":")[0]
				if tag == config.Container.Image {
					shouldPullImage = false
					break
				}
			}
		}
	}

	if shouldPullImage {
		err = client.PullImage(imageOpts, os.Stdout)
	}
	return
}

// Instantiate a service from the provided configuration.
func instantiateService(client *dockerclient.Client, config *ServiceConfig, instanceNum int) (err error) {
	name := config.FullyQualifiedDomainName(instanceNum)
	creationOptions := dockerclient.CreateContainerOptions{Name: name}
	containerConfig := &docker.Config{
		AttachStderr:    config.Container.AttachStderr,
		AttachStdin:     config.Container.AttachStdin,
		AttachStdout:    config.Container.AttachStdout,
		Cmd:             config.Container.Cmd,
		CpuShares:       config.Container.CpuShares,
		Dns:             config.Container.Dns,
		Domainname:      config.Container.Domainname,
		Entrypoint:      config.Container.Entrypoint,
		Env:             config.Container.Env,
		ExposedPorts:    config.Container.ExposedPorts,
		Hostname:        config.Container.Hostname + "i" + strconv.Itoa(instanceNum),
		Image:           config.Container.Image,
		Memory:          config.Container.Memory,
		MemorySwap:      config.Container.MemorySwap,
		NetworkDisabled: config.Container.NetworkDisabled,
		OpenStdin:       config.Container.OpenStdin,
		PortSpecs:       config.Container.PortSpecs,
		StdinOnce:       config.Container.StdinOnce,
		Tty:             config.Container.Tty,
		User:            config.Container.User,
		Volumes:         config.Container.Volumes,
		VolumesFrom:     config.Container.VolumesFrom,
		WorkingDir:      config.Container.WorkingDir,
	}

	// Add internal DNS
	dnsAddrs, err := HostIp()
	if err != nil {
		return
	}
	containerConfig.Dns = append(containerConfig.Dns, dnsAddrs.String())

	// Check if the container already exists and therefore whether it needs to be stopped.
	removeContainer(client, config, instanceNum)

	// Create the new container with the new configuration
	container, err := client.CreateContainer(creationOptions, containerConfig)
	if err != nil {
		return
	}

	hostConfig := &docker.HostConfig{PublishAllPorts: true}

	// Start the new container.
	err = client.StartContainer(container.ID, hostConfig)
	if err != nil {
		return
	}

	// Heartbeat the container.
	instance, err := instanceFromContainer(name, container)
	if err != nil {
		return
	}

	Instances.Heartbeats <- instance
	return
}

func removeContainer(client *dockerclient.Client, config *ServiceConfig, instanceNum int) (err error) {
	name := config.FullyQualifiedDomainName(instanceNum)
	container, err := client.InspectContainer(name)
	if err != nil {
		return
	}
	// Stop or kill the named container.
	err = client.StopContainer(name, ContainerStopTimeout)
	if err != nil {
		err = client.KillContainer(name)
		if err != nil {
			return
		}
	}

	// Remove the stopped container, ignoring any potential error.
	err = client.RemoveContainer(name)
	if err != nil {
		return
	}

	// Notify that the instance has stopped.
	instance, err := instanceFromContainer(name, container)
	if err != nil {
		return
	}

	Instances.Flatlines <- instance
	return
}
