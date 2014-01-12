package daprdockr

import (
	"github.com/coreos/go-etcd/etcd"
	"github.com/dotcloud/docker"
	dockerclient "github.com/fsouza/go-dockerclient"
	"log"
	"strconv"
	"strings"
	"time"
)

const (
	DockerWatcherPollInterval = 5 // Seconds.
)

func PushStateChangesIntoStore(dockerClient *dockerclient.Client, etcdClient *etcd.Client, stop chan bool) {
	for {
		select {
		case <-stop:
			break
		case <-time.After(DockerWatcherPollInterval * time.Second):
			containers, err := getContainers(dockerClient)

			if err != nil {
				log.Printf("[DockerWatcher] Error getting containers: %s.\n", err)
				continue
			}
			for _, container := range containers {
				if !containerIsManaged(container.Names) {
					// This container isn't managed by this system.
					continue
				}
				instance, err := instanceFromAPIContainer(&container)
				if err != nil {
					log.Printf("[DockerWatcher] Updated deriving instance from container %s for %s.\n", container, err)
					continue
				}
				Instances.Heartbeats <- instance
			}
		}
	}

	log.Printf("[DockerWatcher] Exiting.\n")
}

func containerInstanceName(names []string) (result string) {
	for _, name := range names {
		if strings.HasSuffix(name, ContainerDomainSuffix) {
			result = name[1:]
			break
		}
	}
	return
}
func instanceFromContainer(name string, container *docker.Container) (instance *Instance, err error) {
	networkSettings := container.NetworkSettings
	if networkSettings == nil {
		networkSettings = new(docker.NetworkSettings)
	}

	apiContainer := &docker.APIContainers{
		ID:    container.ID,
		Image: container.Image,
		Names: []string{name},
		Ports: networkSettings.PortMappingAPI(),
	}
	return instanceFromAPIContainer(apiContainer)
}
func instanceFromAPIContainer(apiContainer *docker.APIContainers) (instance *Instance, err error) {
	name := strings.Split(containerInstanceName(apiContainer.Names), ".")
	instance = new(Instance)

	instance.Instance, err = strconv.Atoi(name[0])
	instance.Service = name[1]
	instance.Group = name[2]
	hostIp, err := HostIp()
	if err != nil {
		return
	}
	instance.Addrs = []string{hostIp.String()}
	instance.PortMappings = make(map[string]string)
	for _, portMapping := range apiContainer.Ports {
		private := strconv.FormatInt(portMapping.PrivatePort, 10)
		public := strconv.FormatInt(portMapping.PublicPort, 10)
		instance.PortMappings[private] = public
	}
	return
}

func containerIsManaged(names []string) bool {
	for _, name := range names {
		if strings.HasSuffix(name, ContainerDomainSuffix) {
			return true
		}
	}
	return false
}

func getContainers(client *dockerclient.Client) (containers []docker.APIContainers, err error) {
	listContainersOptions := dockerclient.ListContainersOptions{}
	return client.ListContainers(listContainersOptions)
}
