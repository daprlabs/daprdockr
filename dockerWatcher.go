package main

import (
	goerrors "errors"
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

func PushStateChangesIntoStore(dockerClient *dockerclient.Client, etcdClient *etcd.Client, stop chan bool, errors *chan error) {
	managedContainers := watchManagedContainers(dockerClient, stop, errors)
	defer close(managedContainers)
	for containers := range managedContainers {
		for _, container := range containers {
			instance, err := instanceFromContainer(container)
			if err != nil && errors != nil {
				*errors <- err
				continue
			}
			err = UpdateInstance(etcdClient, instance)
			if err != nil && errors != nil {
				*errors <- err
				continue
			}
			log.Printf("[DockerWatcher] Updated entry for %s.\n", instance)
		}
	}
	if errors != nil {
		*errors <- goerrors.New("Exiting PushStateChangesIntoStore")
	}
}

func watchManagedContainers(client *dockerclient.Client, stop chan bool, errors *chan error) (managedContainers chan []docker.APIContainers) {
	managedContainers = make(chan []docker.APIContainers, 10)
	go func() {
		defer close(managedContainers)
		for containers := range watchContainers(client, stop, errors) {
			currentManagedContainers := make([]docker.APIContainers, 0, 5)
			for _, container := range containers {
				if containerIsManaged(container.Names) {
					currentManagedContainers = append(currentManagedContainers, container)
				}
			}

			managedContainers <- currentManagedContainers
		}
		if errors != nil {
			*errors <- goerrors.New("Exiting watchManagedContainers")
		}
	}()

	return
}

func watchContainers(client *dockerclient.Client, stop chan bool, errors *chan error) (containers chan []docker.APIContainers) {
	containers = make(chan []docker.APIContainers, 10)
	go func() {
		defer close(containers)
		for {
			select {
			case <-stop:
				break
			case <-time.After(DockerWatcherPollInterval * time.Second):
				newContainers, err := getContainers(client)
				if err != nil {
					if errors != nil {
						*errors <- err
					}
					continue
				} else {
					containers <- newContainers
				}
			}
		}
		if errors != nil {
			*errors <- goerrors.New("Exiting watchContainers")
		}
	}()

	return
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
