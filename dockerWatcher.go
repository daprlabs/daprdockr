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

// Cache the local IP address.
var localIPs, localIPsErr = InternetRoutedIPs()

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
				instance, err := instanceFromContainer(container)
				if err != nil {
					log.Printf("[DockerWatcher] Updated deriving instance from container %s for %s.\n", container, err)
					continue
				}
				err = UpdateInstance(etcdClient, instance)
				if err != nil {
					log.Printf("[DockerWatcher] Error updating instance %s: %s.\n", instance, err)
					continue
				}
				log.Printf("[DockerWatcher] Heartbeat %s.\n", instance.QualifiedName())
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

func instanceFromContainer(container docker.APIContainers) (instance *Instance, err error) {
	name := strings.Split(containerInstanceName(container.Names), ".")
	instance = new(Instance)

	instance.Instance, err = strconv.Atoi(name[0])
	instance.Service = name[1]
	instance.Group = name[2]
	instance.Addrs, err = localIPs, localIPsErr
	if err != nil {
		return
	}
	instance.PortMappings = make(map[string]string)
	for _, portMapping := range container.Ports {
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
