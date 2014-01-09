package main

import (
	"encoding/json"
	"github.com/coreos/go-etcd/etcd"
	"strings"
)

// Uniquely identifies a service.
type ServiceIdentifier struct {
	Name  string
	Group string
}

func (id *ServiceIdentifier) QualifiedName() string {
	return id.Name + "." + id.Group
}

func (id *ServiceIdentifier) Key() string {
	return id.GroupKey() + "/" + id.Name
}

func (id *ServiceIdentifier) GroupKey() string {
	return "config/services/" + id.Group
}

type ServiceHttpConfig struct {
	HostName      string
	ContainerPort uint16
}

type ServiceContainerConfig struct {
	Image   string
	Command []string
}

type ServiceConfig struct {
	ServiceIdentifier `json:"-"`
	Instances         int

	Container ServiceContainerConfig
	// The Docker container image used to pull and run the container
	Http ServiceHttpConfig
	// TODO: Add [Web] hooks?
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

/* 9/Jan/2014 Reuben: Commented out because we cannot handle change messages dealing with entire groups.
// Removes a group of services.
func DeleteServiceGroup(client *etcd.Client, id *ServiceIdentifier) (err error) {
	_, err = client.DeleteDir(id.GroupKey())
	return
} */

func DesiredServices() (desiredServices chan map[string]*ServiceConfig) {
	return
}

// Returns a channel publishing the current service configs whenever they change.
func CurrentServiceConfigs(client *etcd.Client, stop chan bool, errors *chan error) (currentServiceConfigs chan map[string]*ServiceConfig) {
	serviceConfigMap := make(map[string]*ServiceConfig)
	applyUpdate := func(update *ServiceConfigUpdate) {
		name := update.ServiceConfig.QualifiedName()
		switch update.Operation {
		case Add:
			serviceConfigMap[name] = update.ServiceConfig
		case Remove:
			delete(serviceConfigMap, name)
		}
		return
	}

	updated := func(update *ServiceConfigUpdate) map[string]*ServiceConfig {
		applyUpdate(update)
		return serviceConfigMap
	}

	currentServiceConfigs = make(chan map[string]*ServiceConfig)
	go func() {
		for update := range ServiceConfigUpdates(client, stop, errors) {
			// Mutate the current service configs collection
			currentServiceConfigs <- updated(update)
		}
	}()

	return
}

// Returns a channel of all service configuration updates.
func ServiceConfigUpdates(client *etcd.Client, stop chan bool, errors *chan error) (updates chan *ServiceConfigUpdate) {
	updates = make(chan *ServiceConfigUpdate)
	incomingUpdates := make(chan *etcd.Response)
	go client.Watch("config/services", 0, true, incomingUpdates, stop)
	go func() {
		for update := range incomingUpdates {
			config, err := parseServiceConfigUpdate(update)
			if err != nil && errors != nil {
				*errors <- err
			} else {
				updates <- config
			}
		}

		close(incomingUpdates)
	}()
	return
}

// Parses a service config from an update response and returns the config.
func parseServiceConfigUpdate(response *etcd.Response) (update *ServiceConfigUpdate, err error) {
	keyParts := strings.Split(response.Node.Key, "/")[2:]
	update = new(ServiceConfigUpdate)

	update.Operation, err = parseActionToOperation(response.Action)
	if err != nil {
		return
	}

	serviceConfig := new(ServiceConfig)
	serviceConfig.Group = keyParts[0]
	serviceConfig.Name = keyParts[1]
	if err != nil {
		return
	}

	// Do not attempt to parse value if it is not present.
	if len(response.Node.Value) > 0 {
		err = json.Unmarshal([]byte(response.Node.Value), serviceConfig)
		if err != nil {
			return
		}
	}

	update.ServiceConfig = serviceConfig
	return
}
