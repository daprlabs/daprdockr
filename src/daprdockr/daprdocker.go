package main

import (
	"encoding/json"
	"fmt"
	"github.com/coreos/go-etcd/etcd"
	"os"
	"os/signal"
	"strconv"
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
	client := etcd.NewClient([]string{"http://192.168.1.10:5003", "http://192.168.1.10:5002", "http://192.168.1.10:5001"})
	stop := make(chan bool)
	errors := make(chan error)
	updateDns := make(chan map[string]*Instance)

	go func() {
		for err := range errors {
			fmt.Printf("Error: %s\n", err)
		}
	}()
	go func() {
		for instances := range CurrentInstances(client, stop, &errors) {
			// Update DNS
			updateDns <- instances

			UpdateDns(instances)

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
		for required := range RequiredStateChanges(client, stop, &errors) {
			js, err := json.Marshal(required)
			if err != nil {
				fmt.Printf("Marshalling failed: %s", err)
				return
			}

			fmt.Printf("REQUIRED: %s \n\n", js)
		}
	}()

	go func() {
		for i := 0; i < 12; i++ {
			instance := strconv.Itoa(i)
			service := []string{"web", "db"}[i%2]
			group := "freebay-" + []string{"prod", "ppe", "test"}[i%3]

			response, err := client.Set("instances/"+group+"/"+service+"/"+instance, "127.0.0.1", 50)
			if err != nil {
				fmt.Printf("Error: %s\n", err.Error())
			}
			fmt.Printf("[%s] Key: %s Value: %s\n", response.Action, response.Node.Key, response.Node.Value)
		}
		stop <- true
	}()

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

func UpdateDns(instances map[string]*Instance) (err error) {
	return
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
