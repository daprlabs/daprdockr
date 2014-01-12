package main

import (
	"flag"
	"github.com/ReubenBond/daprdockr"
	"github.com/coreos/go-etcd/etcd"
	"github.com/fsouza/go-dockerclient"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime/pprof"
	"strings"
	"syscall"
	"time"
)

const (
	UpdateThrottleInterval = 2 // Seconds
	DockerSockFlag         = "docker"
	DockerSockEnv          = "DOCKER_SOCK"
	EtcdHostsFlag          = "etcd"
	EtcdHostsEnv           = "ETCD_HOSTS"
	HostIpFlag             = "host"
	HostIpEnv              = "HOST_IP"
)

var etcdHostsFlag = flag.String(EtcdHostsFlag,
	"",
	"\n\tComma separated list of URLs of the cluster's etcd.\n\tOverrides "+EtcdHostsEnv+" environment variable.\n\tExample: http://localhost:5001,http://localhost:5002,http://localhost:5003")
var dockerSockFlag = flag.String(DockerSockFlag,
	"",
	"Docker's remote API endpoint. Overrides "+DockerSockEnv+" environment variable.")
var hostIpFlag = flag.String(HostIpFlag,
	"",
	"Docker host IP address. Overrides "+HostIpEnv+" environment variable.")
var routeFile = flag.String("route", "/proc/net/route", "Location of the container host's route file.")
var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")

func getFlagOrEnv(flagName, envName string) string {
	envVal := os.Getenv(envName)
	log.Printf("[%s]=\"%s\"", envName, envVal)
	flagVal := flag.Lookup(flagName).Value.String()
	log.Printf("[%s]=\"%s\"", flagName, flagVal)
	if flagVal == "" {
		return envVal
	}
	return flagVal
}

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

	etcdHosts := getFlagOrEnv(EtcdHostsFlag, EtcdHostsEnv)
	dockerSock := getFlagOrEnv(DockerSockFlag, DockerSockEnv)
	hostIpStr := getFlagOrEnv(HostIpFlag, HostIpEnv)

	var hostIp net.IP

	if hostIpStr != "" {
		hostIp = net.ParseIP(hostIpStr)
	} else {
		hostIp, err = daprdockr.InternetRoutedIp()
		if err != nil {
			log.Printf("[DaprDockr] Failed to discover host IP: %s.\n", err)
			return
		}
	}
	daprdockr.SetHostIp(hostIp)

	log.Print("[DaprDockr] etcd: ", etcdHosts)
	log.Print("[DaprDockr] docker: ", dockerSock)
	log.Print("[DaprDockr] route file: ", *routeFile)
	log.Print("[DaprDockr] Host IP: ", hostIp)

	etcdAddrs := strings.Split(etcdHosts, ",")
	etcdClient := etcd.NewClient(etcdAddrs)

	stop := make(chan bool)

	dockerClient, err := docker.NewClient(dockerSock)
	if err != nil {
		log.Printf("[DaprDockr] Failed to create Docker client at address %s: %s.\n", dockerSock, err)
		return
	}

	daprdockr.Route4FilePath = *routeFile

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
