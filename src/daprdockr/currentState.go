package main

import (
	"encoding/json"
	"errors"
	"github.com/coreos/go-etcd/etcd"
	"net"
	"strconv"
	"strings"
)

type Instance struct {
	Group    string `json:"-"`
	Service  string `json:"-"`
	Instance int    `json:"-"`
	Addrs    []net.IP
	//PortMappings map[uint16]uint16
}

func (this *Instance) FullyQualifiedDomainName() string {
	return strconv.Itoa(this.Instance) + "." + this.Service + "." + this.Group + "." + CONTAINER_DOMAIN_SUFFIX
}

func (this *Instance) QualifiedName() string {
	return strconv.Itoa(this.Instance) + "." + this.Service + "." + this.Group
}

type Operation int

const (
	Add Operation = iota
	Remove
)

func (op Operation) String() (result string) {
	switch op {
	case Add:
		result = "add"
	case Remove:
		result = "remove"
	}
	return
}

type InstanceUpdate struct {
	Operation Operation
	Instance  *Instance
}

func UpdateInstance(client *etcd.Client, instance *Instance) (err error) {

	payload, err := json.Marshal(instance)
	if err != nil {
		return
	}
	_, err = client.Set("instances/"+instance.Group+"/"+instance.Service+"/"+strconv.Itoa(instance.Instance), string(payload), 10)
	if err != nil {
		return
	}
	return
}

// Returns a channel publishing the current instances whenever they change.
func CurrentInstances(client *etcd.Client, stop chan bool, errors *chan error) (currentInstances chan map[string]*Instance) {
	currentInstancesMap := make(map[string]*Instance)
	applyUpdate := func(update *InstanceUpdate) {
		name := update.Instance.FullyQualifiedDomainName()
		switch update.Operation {
		case Add:
			currentInstancesMap[name] = update.Instance
		case Remove:
			delete(currentInstancesMap, name)
		}
		return
	}

	updated := func(update *InstanceUpdate) map[string]*Instance {
		applyUpdate(update)
		return currentInstancesMap
	}

	currentInstances = make(chan map[string]*Instance)
	go func() {
		for update := range InstanceUpdates(client, stop, errors) {
			// Mutate the current instances collection
			currentInstances <- updated(update)
		}
	}()

	return
}

// Returns a channel of all instance updates.
func InstanceUpdates(client *etcd.Client, stop chan bool, errors *chan error) (instances chan *InstanceUpdate) {
	instances = make(chan *InstanceUpdate)
	instanceUpdates := make(chan *etcd.Response)
	go client.Watch("instances", 0, true, instanceUpdates, stop)
	go func() {
		for update := range instanceUpdates {
			instance, err := parseInstanceUpdate(update)
			if err != nil && errors != nil {
				*errors <- err
			} else {
				instances <- instance
			}
		}

		close(instanceUpdates)
	}()
	return
}

func parseActionToOperation(action string) (operation Operation, err error) {
	switch action {
	case "set":
		fallthrough
	case "update":
		fallthrough
	case "create":
		fallthrough
	case "compareAndSwap":
		operation = Add
	case "delete":
		fallthrough
	case "expire":
		operation = Remove
	default:
		err = errors.New("Invalid action: " + action)
	}
	return
}

// Parses an instance from an update response and returns the instance.
func parseInstanceUpdate(update *etcd.Response) (instanceUpdate *InstanceUpdate, err error) {
	keyParts := strings.Split(update.Node.Key, "/")[2:]
	instanceUpdate = new(InstanceUpdate)

	instanceUpdate.Operation, err = parseActionToOperation(update.Action)
	if err != nil {
		return
	}

	instance := new(Instance)

	instance.Group = keyParts[0]
	instance.Service = keyParts[1]
	instance.Instance, err = strconv.Atoi(keyParts[2])
	if err != nil {
		return
	}

	// Do not attempt to parse value if it is not present.
	if len(update.Node.Value) > 0 {
		err = json.Unmarshal([]byte(update.Node.Value), instance)
		if err != nil {
			return
		}
	}

	instanceUpdate.Instance = instance
	return
}
