package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/coreos/go-etcd/etcd"
	"github.com/daprlabs/daprdockr"
	"os"
	"strings"
)

var etcdAddresses = flag.String("etcd", "http://localhost:5001,http://localhost:5002,http://localhost:5003", "Comma separated list of URLs of the cluster's etcd.")
var set = flag.Bool("set", false, "Set service configuration.")
var get = flag.Bool("get", true, "Get service configuration.")
var del = flag.Bool("del", false, "Delete service configuration.")

var verbose = flag.Bool("v", false, "Provide verbose output.")
var printIp = flag.Bool("ip", false, "Prints the local \"Internet routed\" IP.")
var service = flag.String("svc", "", "The service to operate on, in the form \"<service>.<group>\".")

var instances = flag.Int("instances", 0, "The target number of service instances.")
var image = flag.String("image", "", "The service image in the form accepted by docker.")
var cmd = flag.String("cmd", "", "The command to run in the container.")
var httpPort = flag.String("http-port", "", "The HTTP port within the container for load balancing.")
var httpHostName = flag.String("http-host", "", "The HTTP hostname used for load balancing this service.")
var stdIn = flag.Bool("stdin", false, "Read JSON service definition from stdin.")

func main() {
	flag.Parse()
	config := new(daprdockr.ServiceConfig)
	var err error

	if *printIp {
		ipAddr, err := daprdockr.InternetRoutedIp()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s.\n", err)
			return
		}

		fmt.Println(ipAddr)
		return
	}

	etcdAddrs := strings.Split(*etcdAddresses, ",")
	etcdClient := etcd.NewClient(etcdAddrs)
	if *verbose {
		fmt.Printf("Etcd nodes:\n\t%s\n", strings.Join(etcdAddrs, "\n\t"))
	}

	if *stdIn {
		decoder := json.NewDecoder(os.Stdin)
		err = decoder.Decode(config)
	} else {
		serviceNameParts := strings.Split(*service, ".")
		if len(serviceNameParts) < 2 {

			flag.Usage()
			return
		}

		if *set || *del {
			*get = false
		}

		config.Instances = *instances
		config.Container.Image = *image //"troygoode/centos-node-hello"
		if *cmd != "" {
			config.Container.Cmd = []string{*cmd}
		}
		config.Http.ContainerPort = *httpPort
		config.Http.HostName = *httpHostName

		config.Name = serviceNameParts[0]
		config.Group = strings.Join(serviceNameParts[1:], "")
	}

	if err == nil {
		if *set {
			if *verbose {
				fmt.Printf("POST %s/%s ...\n", config.Group, config.Name)
			}
			err = daprdockr.SetServiceConfig(etcdClient, config)
		} else if *del {
			if *verbose {
				fmt.Printf("DELETE %s/%s\n", config.Group, config.Name)
			}
			err = daprdockr.DeleteService(etcdClient, &config.ServiceIdentifier)

		}

		if *get || *set {
			if *verbose {
				fmt.Printf("GET %s/%s\n", config.Group, config.Name)
			}
			serviceConfig, err := daprdockr.GetServiceConfig(etcdClient, config.Group, config.Name)
			if err == nil {
				var js []byte
				js, err = json.MarshalIndent(serviceConfig, "", "  ")
				if err == nil {
					fmt.Println(string(js))
				}
			}
		}
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Command failed: %s", err)
		os.Exit(-1)
	}
}
