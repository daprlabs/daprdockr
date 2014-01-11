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
	"strings"
	"syscall"
	"time"
)

const (
	UpdateThrottleInterval = 2 // Seconds
)

var etcdAddresses = flag.String("etcd", "http://localhost:5001,http://localhost:5002,http://localhost:5003", "Comma separated list of URLs of the cluster's etcd.")
var dockerAddress = flag.String("docker", "unix:///var/run/docker.sock", "URLs of the local docker instance.")
var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")

func main() {
	var err error
	flag.Parse()
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	etcdAddrs := strings.Split(*etcdAddresses, ",")
	etcdClient := etcd.NewClient(etcdAddrs)

	stop := make(chan bool)

	dockerClient, err := docker.NewClient(*dockerAddress)
	if err != nil {
		log.Printf("[DaprDockr] Failed to create Docker client at address %s: %s.\n", *dockerAddress, err)
	}

	// Push changes from the local Docker instance into etcd.
	go daprdockr.PushStateChangesIntoStore(dockerClient, etcdClient, stop)

	// Pull changes to the currently running instances and configurations.
	instanceUpdates := daprdockr.LatestInstances(etcdClient, stop, 3, UpdateThrottleInterval*time.Second)
	serviceConfigUpdates := daprdockr.LatestServiceConfigs(etcdClient, stop, UpdateThrottleInterval*time.Second)

	// Pull required state changes from the store and attempt to apply them locally.
	requiredChanges := daprdockr.RequiredStateChanges(instanceUpdates[0], serviceConfigUpdates, stop)
	go daprdockr.ApplyRequiredStateChanges(dockerClient, etcdClient, requiredChanges, stop)

	errors := make(chan error, 100)
	go func() {
		for err := range errors {
			if err != nil {
				log.Printf("[DaprDockr] Error: %s.\n", err)
			}
		}
	}()

	// Start a DNS server so that the addresses of service instances can be resolved.
	go daprdockr.StartDnsServer(instanceUpdates[1], &errors)

	// Start an HTTP load balancer so that configured sites can be correctly served.
	go daprdockr.StartLoadBalancer(etcdClient, instanceUpdates[2], stop, &errors)

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
