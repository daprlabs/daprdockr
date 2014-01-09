package main

import (
	"github.com/coreos/go-etcd/etcd"
	"github.com/dotcloud/docker"
	dockerclient "github.com/fsouza/go-dockerclient"
	"os"
	"strconv"
	"strings"
)

const (
	ContainerStopTimeout = 30 // seconds
)

// Pull required state changes from the store and attempt to apply them locally.
func ApplyRequiredStateChanges(dockerClient *dockerclient.Client, etcdClient *etcd.Client, stop chan bool, errors *chan error) {
	for required := range RequiredStateChanges(etcdClient, stop, errors) {
		for _, change := range required {
			if change.Operation == Add {
				err := <-prepareForService(dockerClient, change.ServiceConfig)
				if err != nil {
					if errors != nil {
						*errors <- err
					}
					continue
				}

				// TODO: Acquire a lock before instantiation
				ready := instantiateService(dockerClient, change.ServiceConfig, change.Instance)
				err = <-ready
				close(ready)
				if err != nil && errors != nil {
					*errors <- err
				}
			}
		}
	}
}

// Prepares for a service to be instantiated by pulling the container's image.
func prepareForService(client *dockerclient.Client, config *ServiceConfig) (ready chan error) {
	ready = make(chan error)
	go func() {
		//TODO: Account for different registries.
		imageOpts := dockerclient.PullImageOptions{Repository: config.Container.Image}
		images, err := client.ListImages(true)
		if err != nil {
			ready <- err
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
		ready <- err
	}()
	return
}

// Instantiate a service from the provided configuration.
func instantiateService(client *dockerclient.Client, config *ServiceConfig, instance int) (ready chan error) {
	ready = make(chan error)
	go func() {
		name := config.FullyQualifiedDomainName(instance)
		creationOptions := dockerclient.CreateContainerOptions{Name: name}
		containerConfig := &docker.Config{
			Hostname:   "i" + strconv.Itoa(instance),
			Domainname: config.QualifiedName(),
			Cmd:        config.Container.Command,
			Image:      config.Container.Image,
		}

		// Stop or kill the named container.
		err := client.StopContainer(name, ContainerStopTimeout)
		if err != nil {
			err = client.KillContainer(name)
			if err != nil {
				ready <- err
				return
			}
		}

		// Remove the stopped container, ignoring any potential error.
		err = client.RemoveContainer(name)

		// Create the new container with the new configuration
		container, err := client.CreateContainer(creationOptions, containerConfig)
		if err != nil {
			ready <- err
			return
		}

		hostConfig := &docker.HostConfig{}

		// Start the new container.
		err = client.StartContainer(container.ID, hostConfig)
		if err != nil {
			ready <- err
			return
		}

		ready <- err
	}()
	return
}
