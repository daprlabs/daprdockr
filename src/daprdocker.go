package main

import "fmt"
import "strconv"
import "github.com/coreos/go-etcd/etcd"

func main() {
	fmt.Printf("Hello world!\n")
	c := etcd.NewClient([]string{"http://192.168.1.10:5003", "http://192.168.1.10:5002", "http://192.168.1.10:5001"})

	response, err := c.Get("test", false, false)
	if err != nil {

		fmt.Printf("Error: %s\n", err.Error())
	}
	fmt.Printf("[%s] Key: %s Value: %s\n", response.Action, response.Node.Key, response.Node.Value)

	instanceUpdates := make(chan *etcd.Response)
	stop := make(chan bool)
	go c.Watch("instances", 0, true, instanceUpdates, stop)
	go func() {
		for i := 0; i < 10; i++ {
			response := <-instanceUpdates
			fmt.Printf("UPDATE [%s] Key: %s Value: %s\n", response.Action, response.Node.Key, response.Node.Value)
		}
		stop <- true
	}()

	go func() {
		for i := 0; i < 12; i++ {
			val := strconv.Itoa(i)
			response, err := c.Set("instances", val, 50)
			if err != nil {
				fmt.Printf("Error: %s\n", err.Error())
			}
			fmt.Printf("[%s] Key: %s Value: %s\n", response.Action, response.Node.Key, response.Node.Value)
		}
	}()
	<-stop
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
