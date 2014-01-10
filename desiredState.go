package main

import (
	"encoding/json"
	goerrors "errors"
	"github.com/coreos/go-etcd/etcd"
	"strconv"
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

func (id *ServiceIdentifier) FullyQualifiedDomainName(instance int) string {
	return strconv.Itoa(instance) + "." + id.QualifiedName() + "." + ContainerDomainSuffix
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
		defer close(currentServiceConfigs)
		for update := range serviceConfigUpdates(client, stop, errors) {
			// Mutate the current service configs collection
			currentServiceConfigs <- updated(update)
		}
		if errors != nil {
			*errors <- goerrors.New("Exiting CurrentServiceConfigs")
		}
	}()

	return
}
func getServiceConfigs(client *etcd.Client, errors *chan error, serviceConfigs chan *ServiceConfigUpdate) {
	response, err := client.Get("config/services", false, true)
	if err != nil && errors != nil {
		*errors <- err
		return
	}
	for _, node := range response.Node.Nodes {
		r, err := client.Get(node.Key, false, true)
		if err != nil && errors != nil {
			*errors <- err
			continue
		}
		for _, iNode := range r.Node.Nodes {
			serviceConfig, err := parseServiceConfig(&iNode)
			if err != nil && errors != nil {
				*errors <- err
				continue
			}

			serviceConfigUpdate := new(ServiceConfigUpdate)
			serviceConfigUpdate.Operation = Add
			serviceConfigUpdate.ServiceConfig = serviceConfig
			serviceConfigs <- serviceConfigUpdate
		}
	}
}

// Returns a channel of all service configuration updates.
func serviceConfigUpdates(client *etcd.Client, stop chan bool, errors *chan error) (updates chan *ServiceConfigUpdate) {
	updates = make(chan *ServiceConfigUpdate)
	incomingUpdates := make(chan *etcd.Response)
	go getServiceConfigs(client, errors, updates)
	go client.Watch("config/services", 0, true, incomingUpdates, stop)
	go func() {
		defer close(incomingUpdates)
		for update := range incomingUpdates {
			config, err := parseServiceConfigUpdate(update)
			if err != nil && errors != nil {
				*errors <- err
			} else {
				updates <- config
			}
		}

		if errors != nil {
			*errors <- goerrors.New("Exiting CurrentServiceConfigs")
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
