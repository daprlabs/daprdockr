package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"syscall"
	"text/template"
)

const (
	NginxConfigFilePerms os.FileMode = 0644
)

// TODO: Load from file
const NginxConfigurationTemplate = `
events { 
	use epoll; 
	worker_connections 51200; 
}

http { {{range . }}
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

func StartLoadBalancer(currentInstances chan map[string]*Instance, stop chan bool, errorChan *chan error) {
	reload := make(chan bool, 1)
	defer close(reload)
	start := make(chan bool, 1)
	defer close(start)

	// Keep load balancer running.
	go func() {
		var cmd *exec.Cmd
		for {
			select {
			case _, ok := <-stop:
				if !ok {
					break
				}
				break
			case _, ok := <-start:
				if !ok {
					break
				}

				// The process is not running, run it.
				fmt.Println("Starting Load Balancer")
				exec.Command("nginx", "-g", "pid "+pidFile+";", "-s", "stop").Run()
				cmd = exec.Command("nginx", "-g", "daemon off; pid "+pidFile+";", "-c", configFile)
				go runLoadBalancer(cmd, start, errorChan)
			case _, ok := <-reload:
				if !ok {
					break
				}

				// Reload configuration.
				fmt.Println("Reloading Load Balancer")
				if cmd != nil && cmd.Process != nil {
					cmd.Process.Signal(syscall.SIGHUP)
				} else {
					start <- true
				}
			}
		}
	}()

	// Initially run the load balancer
	for {
		select {
		case _, ok := <-stop:
			if !ok {
				break
			}
			break
		case instances, ok := <-currentInstances:
			if !ok {
				break
			}

			err := updateLoadBalancerConfig(instances)
			if err != nil && errorChan != nil {
				*errorChan <- err
				continue
			}
			reload <- true

		}
	}
	fmt.Println("Exiting Load Balancer")
}
func runLoadBalancer(cmd *exec.Cmd, restart chan bool, errorChan *chan error) {
	err := cmd.Run()
	if err != nil && errorChan != nil {
		fmt.Println("run errored")
		*errorChan <- err
	}
	fmt.Println("LOAD BALANCER DIED")
	restart <- true
}

func updateLoadBalancerConfig(currentInstances map[string]*Instance) (err error) {
	err = nginxConfigurationTemplateErr

	// derrive sites from current instances.

	sites := []SiteConfig{
		{
			Name:    "service.com",
			Servers: []string{"google.com", "yahoo.com", "bing.com"},
		},
		{
			Name:    "other-service.com",
			Servers: []string{"twitter.com", "plus.google.com", "facebook.com"},
		},
	}
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
