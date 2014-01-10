package daprdockr

import (
	"bytes"
	"github.com/coreos/go-etcd/etcd"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"text/template"
	"time"
)

const (
	NginxConfigFilePerms            os.FileMode = 0644
	LoadBalancerWaitBetweenLaunches             = 5 // Seconds
)

// TODO: Load from file
// TODO: Give higher weight to backends which are on the local machine. Alternatively, mark all remote hosts as 'backup' servers.
const NginxConfigurationTemplate = `
events { 
	use epoll; 
	worker_connections 51200; 
}

http {
	resolver 127.0.0.1;
	{{range . }}
	upstream {{.Name}}.lb { {{range .Servers}}
		server {{.}};{{end}}
	}
	server {
		listen 80;
		server_name {{.Name}};
		location / {
			proxy_pass http://{{.Name}}.lb;
		}
	}
	{{end}}
}`

var nginxConfigurationTemplate, nginxConfigurationTemplateErr = template.New("HTTP Load Balancer Configuration").Parse(NginxConfigurationTemplate)

type SiteConfig struct {
	Name    string
	Servers []string
}

// TODO: Monitor & restart
//
//

const pidFile = "/tmp/nginx.pid"
const configFile = "/tmp/nginx.conf"

func StartLoadBalancer(etcdClient *etcd.Client, currentInstances chan map[string]*Instance, stop chan bool, errorChan *chan error) {
	reload := make(chan bool)
	defer close(reload)
	start := make(chan bool, 1)
	defer close(start)

	// Keep load balancer running.
	go func() {
		starting := false
		var cmd *exec.Cmd
		for {
			select {
			case _, _ = <-stop:
				break
			case _, ok := <-start:
				if !ok {
					break
				}

				// The process is not running, run it.
				log.Println("[LoadBalancer] Starting.")
				exec.Command("nginx", "-g", "pid "+pidFile+";", "-s", "stop").Run()
				cmd = exec.Command("nginx", "-g", "daemon off; pid "+pidFile+";", "-c", configFile)
				starting = true
				go runLoadBalancer(cmd, start, errorChan)
			case _, ok := <-reload:
				if !ok {
					break
				}

				// Reload configuration.
				log.Println("[LoadBalancer] Reloading configuration.")
				if cmd != nil && cmd.Process != nil {
					starting = false
					cmd.Process.Signal(syscall.SIGHUP)
				} else if !starting {
					start <- true
				}
			}
		}
	}()

	// Initially run the load balancer
	for {
		select {
		case _, _ = <-stop:
			break
		case instances, ok := <-currentInstances:
			if !ok {
				break
			}

			err := updateLoadBalancerConfig(etcdClient, instances)
			if err != nil && errorChan != nil {
				*errorChan <- err
				continue
			}
			reload <- true

		}
	}
	log.Println("[LoadBalancer] Stopping.")
}

func runLoadBalancer(cmd *exec.Cmd, restart chan bool, errorChan *chan error) {
	err := cmd.Run()
	if err != nil && errorChan != nil {
		*errorChan <- err
	}
	log.Println("[LoadBalancer] Process died.")

	// Avoid quickly successive failures.
	<-time.After(10 * time.Second)
	restart <- true
}

func updateLoadBalancerConfig(client *etcd.Client, currentInstances map[string]*Instance) (err error) {
	err = nginxConfigurationTemplateErr
	siteMap := make(map[string][]string) // map of public hostname to private address

	// Derive sites from current instances.
	for _, instance := range currentInstances {
		if len(instance.Addrs) == 0 {
			log.Printf("[LoadBalancer] Skipping instance with no known addresses: %s.\n", instance.QualifiedName())
			continue
		}

		config, err := GetServiceConfig(client, instance.Group, instance.Service)
		if err != nil {
			return err
		}

		if len(config.Http.HostName) == 0 {
			log.Printf("[LoadBalancer] Skipping instance with no configured hostname: %s.\n", instance.QualifiedName())
			// This isn't a site container.
			continue
		}

		fqdn := instance.Addrs[0].String() //instance.FullyQualifiedDomainName()
		port := instance.PortMappings[config.Http.ContainerPort]
		site, exists := siteMap[config.Http.HostName]
		if !exists {
			site = make([]string, 0, 4)
			siteMap[config.Http.HostName] = site
		}

		siteMap[config.Http.HostName] = append(siteMap[config.Http.HostName], fqdn+":"+port)
	}

	// Group sites by their hostname (eg: www.thegulaghypercloud.com).
	sites := make([]SiteConfig, 0, len(siteMap))
	for host, servers := range siteMap {
		if len(servers) == 0 {
			continue
		}

		log.Printf("[LoadBalancer] Updating configuration for %s (upstream: %s).\n", host, strings.Join(servers, ", "))
		sites = append(sites, SiteConfig{Name: host, Servers: servers})
	}

	// Create and write the load balancer configuration file.
	config, err := createLoadBalancerConfig(sites)
	if err != nil {
		return
	}

	err = ioutil.WriteFile(configFile, []byte(config), NginxConfigFilePerms)
	return
}

func createLoadBalancerConfig(sites []SiteConfig) (result string, err error) {
	buffer := new(bytes.Buffer)
	err = nginxConfigurationTemplate.Execute(buffer, sites)
	if err == nil {
		result = buffer.String()
	}
	return
}
