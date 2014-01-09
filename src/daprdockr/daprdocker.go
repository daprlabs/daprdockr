package main

import (
	//	"encoding/json"
	"fmt"
	"github.com/coreos/go-etcd/etcd"
	"github.com/fsouza/go-dockerclient"
	"os"
	"os/signal"
	"syscall"
)

const CONTAINER_DOMAIN_SUFFIX = "container"

/*
DaprDocker is an agent for hosting declaratively described services in a cluster.
The system stores both desired and current state in etcd. DaprDocker agents on each host read desired state and race to
satisfy it, using etcd as a distributed lock manager. Heartbeats from agent machines keep the current state up-to-date.

State change hooks allow an internal DNS proxy server to point to individual instances of a service within the cluster,
using the special .container top-level domain.
For example, 1.web.myapp.container will point to instance 1 of the "web" service in the "myapp" group.

State change hooks also allow an Nginx server to load balance HTTP requests over the healthy containers in a Web
service.
*/

func main() {
	etcdClient := etcd.NewClient([]string{"http://192.168.1.10:5003", "http://192.168.1.10:5002", "http://192.168.1.10:5001"})
	stop := make(chan bool)
	errors := make(chan error)
	updateDns := make(chan map[string]*Instance)

	go func() {
		for err := range errors {
			if err != nil {
				fmt.Printf("Error: %s\n", err)
			}
		}
	}()

	dockerClient, err := docker.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		errors <- err
	}

	go func() {
		for instances := range CurrentInstances(etcdClient, stop, &errors) {
			// Update DNS
			updateDns <- instances
			/*js, err := json.Marshal(instances)
			if err != nil {
				fmt.Printf("Marshalling failed: %s", err)
				return
			}

			fmt.Printf("Update: %s \n\n", js)
			*/
		}
		close(errors)
	}()

	go func() {
		for required := range RequiredStateChanges(etcdClient, stop, &errors) {
			/*			js, err := json.Marshal(required)
						if err != nil {
							fmt.Printf("Marshalling failed: %s", err)
							return
						}*/

			fmt.Println("==")
			for key, change := range required {
				fmt.Printf("[%s] %s\n", change.Operation, key)
				if change.Operation == Add {
					err := <-PrepareForService(dockerClient, change.ServiceConfig)
					if err != nil {
						errors <- err
						continue
					}

					errors <- <-InstantiateService(dockerClient, change.ServiceConfig, change.Instance)
				}
			}
			fmt.Println("==")
			/*
				fmt.Printf("REQUIRED: %s \n\n", js)*/
		}
	}()
	/*
		go func() {
			for i := 0; i < 12; i++ {
				instance := strconv.Itoa(i)
				service := []string{"web", "db"}[i%2]
				group := "freebay-" + []string{"prod", "ppe", "test"}[i%3]

				response, err := etcdClient.Set("instances/"+group+"/"+service+"/"+instance, "127.0.0.1", 50)
				if err != nil {
					fmt.Printf("Error: %s\n", err.Error())
				}
				fmt.Printf("[%s] Key: %s Value: %s\n", response.Action, response.Node.Key, response.Node.Value)
			}
			stop <- true
		}()*/

	go func() {
		for i := 0; i < 12; i++ {
			config := new(ServiceConfig)
			config.Group = "freebay-prod"
			config.Instances = 5
			config.Name = "web"
			config.Container.Image = "base/devel"
			config.Container.Command = []string{"/bin/sh", "-c", "sleep 100"}
			SetServiceConfig(etcdClient, config)
		}
		return
	}()

	go UpdateCurrentStateFromManagedContainers(dockerClient, etcdClient, stop, &errors)

	/*
		go func() {
			for containers := range WatchManagedContainers(dockerClient, stop, &errors) {
				for _, container := range containers {
					fmt.Printf("Container: %s @ %s\n\tAKA: %s\n", container.ID, container.Image, strings.Join(container.Names, ", "))
				}
			}
		}()*/

	/*	go func() {

		if err != nil {
			return
		}
		listContainersOptions := docker.ListContainersOptions{All: true}
		containers, err := dockerClient.ListContainers(listContainersOptions)
		if err != nil {
			return
		}

		for _, container := range containers {
			js, err := json.Marshal(container)
			if err != nil {
				return
			}

			fmt.Printf("Container: %s \n\n", js)
		}

		return
	}()*/

	/*go c.Watch("instances", 0, true, instanceUpdates, stop)
	go func() {
		for i := 0; i < 10; i++ {
			response := <-instanceUpdates
			fmt.Printf("UPDATE [%s] Key: %s Value: %s\n", response.Action, response.Node.Key, response.Node.Value)
		}
		stop <- true
	}()*/
	ServeDNS(CONTAINER_DOMAIN_SUFFIX, updateDns)

	// Spin until killed.
	<-stop
	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
forever:
	for {
		select {
		case s := <-sig:
			fmt.Printf("Signal (%d) received, stopping\n", s)
			break forever
		}
	}
}

/*

config/services/
 ... service definitions ...
 service:
   instances: <num>
   hostPrefix: <string>
   group: <string>
   dockerOptions: [<string>]
   image: <string>
   httpHostName: <string>
   ... https info? ...

instances/<group>/<hostPrefix>/<0..instances>
	<ip address> with 10-second TTL

*/
