package main

import (
	"daprdockr"
	"flag"
	"github.com/coreos/go-etcd/etcd"
	"github.com/fsouza/go-dockerclient"
	"log"
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"
)

var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")

func main() {
	flag.Parse()
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	stop := make(chan bool)
	errors := make(chan error, 100)
	go func() {
		for err := range errors {
			if err != nil {
				log.Printf("[DaprDockr] Error: %s.\n", err)
			}
		}
	}()

	// TODO: Make this configurable.
	etcdClient := etcd.NewClient([]string{"http://192.168.1.10:5003", "http://192.168.1.10:5002", "http://192.168.1.10:5001"})
	dockerClient, err := docker.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		log.Printf("[DaprDockr] Failed to create Docker client: %s.\n", err)
	}

	// Push changes from the local Docker instance into etcd.
	go daprdockr.PushStateChangesIntoStore(dockerClient, etcdClient, stop)

	// Pull changes to the currently running instances so that updates can be propagated
	instanceUpdates := []chan map[string]*daprdockr.Instance{
		make(chan map[string]*daprdockr.Instance, 1),
		make(chan map[string]*daprdockr.Instance, 1),
		make(chan map[string]*daprdockr.Instance, 1),
	}

	go func() {
		for instances := range daprdockr.CurrentInstances(etcdClient, stop) {
			for _, ch := range instanceUpdates {
				ch <- instances
			}
		}
	}()

	// Pull required state changes from the store and attempt to apply them locally.
	serviceConfigs := daprdockr.CurrentServiceConfigs(etcdClient, stop)
	requiredChanges := daprdockr.RequiredStateChanges(instanceUpdates[0], serviceConfigs, stop)
	go daprdockr.ApplyRequiredStateChanges(dockerClient, etcdClient, requiredChanges, stop)

	// Start a DNS server so that the addresses of service instances can be resolved.
	go daprdockr.StartDnsServer(instanceUpdates[1], &errors)

	// Start an HTTP load balancer so that configured sites can be correctly served.
	go daprdockr.StartLoadBalancer(etcdClient, instanceUpdates[2], stop, &errors)

	// TODO: remove this.
	// Push in some test data
	/*go func() {
		config := new(ServiceConfig)
		config.Group = "service"
		config.Instances = 2
		config.Name = "web"
		config.Container.Image = "troygoode/centos-node-hello"
		//config.Container.Cmd = []string{"/bin/sh", "-c", "sleep 100"}
		config.Http.ContainerPort = "8080"
		config.Http.HostName = "service.com"
		SetServiceConfig(etcdClient, config)
	}()*/

	// Spin until killed.
	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
forever:
	for {
		select {
		case s := <-sig:
			log.Printf("Signal (%d) received, stopping.\n", s)
			break forever
		}
	}
}
