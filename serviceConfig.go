package daprdockr

import (
	"encoding/json"
	goerrors "errors"
	"github.com/coreos/go-etcd/etcd"
	"github.com/dotcloud/docker"
	"log"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const (
	FullServiceConfigSyncInterval = 65
)

// Uniquely identifies a service.
type ServiceIdentifier struct {
	Name  string
	Group string
}

func (id *ServiceIdentifier) QualifiedName() string {
	return id.Name + "." + id.Group
}
func (id *ServiceIdentifier) FullyQualifiedDomainName(instance int) string {
	return id.InstanceQualifiedName(instance) + "." + ContainerDomainSuffix
}

func (id *ServiceIdentifier) InstanceQualifiedName(instance int) string {
	return strconv.Itoa(instance) + "." + id.QualifiedName()
}

func (id *ServiceIdentifier) Key() string {
	return GetConfigKey(id.Group, id.Name)
}

func GetConfigKey(group, name string) string {
	return "config/services/" + group + "/" + name
}

type ServiceHttpConfig struct {
	HostName      string
	ContainerPort string
}

type ServiceConfig struct {
	ServiceIdentifier
	Instances int
	Container docker.Config
	// The Docker container image used to pull and run the container
	Http ServiceHttpConfig
	// TODO: Add [Web] hooks?
}

func (this *ServiceConfig) Equals(other *ServiceConfig) bool {
	return reflect.DeepEqual(this, other)
}

type ServiceConfigUpdate struct {
	Operation     Operation
	ServiceConfig *ServiceConfig
}

// Adds or updates service configuration.
func SetServiceConfig(client *etcd.Client, config *ServiceConfig) (err error) {
	encodedConfig, err := json.Marshal(config)
	if err != nil {
		return
	}
	_, err = client.Set(config.Key(), string(encodedConfig), 0)
	return
}

// Removes a service.
func DeleteService(client *etcd.Client, id *ServiceIdentifier) (err error) {
	_, err = client.Delete(id.Key(), false)
	return
}

// Returns a channel publishing the current service configs whenever they change.
func CurrentServiceConfigs(client *etcd.Client, stop chan bool) (currentServiceConfigs chan map[string]*ServiceConfig) {
	serviceConfigMap := make(map[string]*ServiceConfig)
	updated := func(update *ServiceConfigUpdate) (newServiceConfigMap map[string]*ServiceConfig, changed bool) {
		newServiceConfigMap = serviceConfigMap
		name := update.ServiceConfig.QualifiedName()
		switch update.Operation {
		case Add:
			if current, exists := newServiceConfigMap[name]; !exists || !current.Equals(update.ServiceConfig) {
				newServiceConfigMap[name] = update.ServiceConfig
				changed = true
			}
		case Remove:
			if _, exists := newServiceConfigMap[name]; exists {
				delete(newServiceConfigMap, name)
				changed = true
			}
		}
		return
	}

	currentServiceConfigs = make(chan map[string]*ServiceConfig)
	go func() {
		defer close(currentServiceConfigs)
		for update := range serviceConfigUpdates(client, stop) {
			// Mutate the current service configs collection
			newServiceConfigMap, changed := updated(update)
			if changed {
				log.Println("[ServiceConfig] Configuration updated.")
				currentServiceConfigs <- newServiceConfigMap
			}
		}
		log.Println("[ServiceConfig] Exiting.")
	}()

	return
}
func GetServiceConfig(client *etcd.Client, group, name string) (config *ServiceConfig, err error) {
	response, err := client.Get(GetConfigKey(group, name), false, false)
	if err != nil {
		return
	}
	config, err = parseServiceConfig(response.Node)
	if err != nil {
		return
	}
	return
}
func getServiceConfigs(client *etcd.Client, serviceConfigs chan *ServiceConfigUpdate) {
	log.Printf("[ServiceConfig] Pulling all configurations.\n")
	response, err := client.Get("config/services", false, true)
	if err != nil {
		log.Printf("[ServiceConfig] Unable to get services: %s.\n", err)
		return
	}
	for _, node := range response.Node.Nodes {
		r, err := client.Get(node.Key, false, true)
		if err != nil {
			log.Printf("[ServiceConfig] Unable to get service configurations: %s.\n", err)
			continue
		}
		for _, iNode := range r.Node.Nodes {
			serviceConfig, err := parseServiceConfig(&iNode)
			if err != nil {
				log.Printf("[ServiceConfig] Unable to parse configuration: %s.\n", err)
				continue
			}

			serviceConfigUpdate := new(ServiceConfigUpdate)
			serviceConfigUpdate.Operation = Add
			serviceConfigUpdate.ServiceConfig = serviceConfig
			serviceConfigs <- serviceConfigUpdate
		}
	}
	log.Printf("[ServiceConfig] Pulled configurations.\n")
}

// Returns a channel of all service configuration updates.
func serviceConfigUpdates(client *etcd.Client, stop chan bool) (updates chan *ServiceConfigUpdate) {
	updates = make(chan *ServiceConfigUpdate)
	incomingUpdates := make(chan *etcd.Response)

	go func() {
		getServiceConfigs(client, updates)
		watchChan := make(chan *etcd.Response)
		go func() {
			for {
				select {
				case <-stop:
					break
				default:
				}
				client.Watch("config/services", 0, true, incomingUpdates, stop)
			}
		}()
		for {
			select {
			case <-stop:
				break
			case <-time.After(FullServiceConfigSyncInterval * time.Second):
				getServiceConfigs(client, updates)
			case update := <-watchChan:
				incomingUpdates <- update
			}
		}
	}()
	go func() {
		defer close(incomingUpdates)
		for update := range incomingUpdates {
			config, err := parseServiceConfigUpdate(update)
			if err != nil {
				log.Printf("[ServiceConfig] Unable to parse update: %s\n", err)
				continue
			} else {
				updates <- config
			}
		}
	}()
	return
}

func parseServiceConfig(node *etcd.Node) (serviceConfig *ServiceConfig, err error) {
	if node == nil {
		err = goerrors.New("Service configuration node missing or node key missing")
		return
	}

	keyParts := strings.Split(node.Key, "/")
	if len(keyParts) < 5 {
		err = goerrors.New("Service configuration node key invalid: " + node.Key)
		return
	}

	keyParts = keyParts[3:]
	serviceConfig = new(ServiceConfig)
	serviceConfig.Group = keyParts[0]
	serviceConfig.Name = keyParts[1]
	if err != nil {
		return
	}

	// Do not attempt to parse value if it is not present.
	if len(node.Value) > 0 {
		err = json.Unmarshal([]byte(node.Value), serviceConfig)
		if err != nil {
			return
		}
	}

	return
}

// Parses a service config from an update response and returns the config.
func parseServiceConfigUpdate(response *etcd.Response) (update *ServiceConfigUpdate, err error) {
	update = new(ServiceConfigUpdate)
	update.Operation, err = parseActionToOperation(response.Action)
	if err != nil {
		return
	}

	update.ServiceConfig, err = parseServiceConfig(response.Node)
	return
}
