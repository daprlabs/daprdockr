package main

import (
	"bytes"
	"encoding/json"
	"errors"
	goerrors "errors"
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

func (this *Instance) Equals(other *Instance) (equal bool) {
	if this == other {
		return true
	}
	equal = this.Group == other.Group && this.Service == other.Service && this.Instance == other.Instance && len(this.Addrs) == len(other.Addrs)
	if !equal {
		return
	}
	for i := range this.Addrs {
		if !bytes.Equal(this.Addrs[i], other.Addrs[i]) {
			equal = false
			return
		}
	}
	return
}

func (this *Instance) FullyQualifiedDomainName() string {
	return strconv.Itoa(this.Instance) + "." + this.Service + "." + this.Group + "." + ContainerDomainSuffix
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

	updated := func(update *InstanceUpdate) (instances map[string]*Instance, changed bool) {
		name := update.Instance.FullyQualifiedDomainName()
		switch update.Operation {
		case Add:
			current, exists := currentInstancesMap[name]
			if !exists || !current.Equals(update.Instance) {
				currentInstancesMap[name] = update.Instance
				changed = true
			}
		case Remove:
			delete(currentInstancesMap, name)
			changed = true
		}
		return
	}

	currentInstances = make(chan map[string]*Instance)
	go func() {
		defer close(currentInstances)
		for update := range instanceUpdates(client, stop, errors) {
			// Mutate the current instances collection and publish it.
			newCurrentInstances, changed := updated(update)
			if changed {
				currentInstances <- newCurrentInstances
			}
		}

		if errors != nil {
			*errors <- goerrors.New("Exiting CurrentInstances")
		}
	}()

	return
}

func getInstances(client *etcd.Client, errors *chan error, instances chan *InstanceUpdate) {
	response, err := client.Get("instances", false, true)
	if err != nil && errors != nil {
		*errors <- err
		return
	}
	for _, n := range response.Node.Nodes {
		for _, node := range n.Nodes {
			r, err := client.Get(node.Key, false, true)
			if err != nil && errors != nil {
				*errors <- err
				continue
			}
			for _, iNode := range r.Node.Nodes {
				instance, err := parseInstance(&iNode)
				if err != nil && errors != nil {
					*errors <- err
					continue
				}

				instanceUpdate := new(InstanceUpdate)
				instanceUpdate.Operation = Add
				instanceUpdate.Instance = instance
				instances <- instanceUpdate
			}
		}
	}
}

// Returns a channel of all instance updates.
func instanceUpdates(client *etcd.Client, stop chan bool, errors *chan error) (instances chan *InstanceUpdate) {
	instances = make(chan *InstanceUpdate)
	updates := make(chan *etcd.Response)
	go getInstances(client, errors, instances)
	go client.Watch("instances", 0, true, updates, stop)
	go func() {
		defer close(updates)
		for update := range updates {
			instance, err := parseInstanceUpdate(update)
			if err != nil && errors != nil {
				*errors <- err
			} else {
				instances <- instance
			}
		}

		if errors != nil {
			*errors <- goerrors.New("Exiting instanceUpdates")
		}
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
func parseInstance(node *etcd.Node) (instance *Instance, err error) {
	if node == nil {
		err = goerrors.New("Instance status node missing or node key missing")
		return
	}

	keyParts := strings.Split(node.Key, "/")
	if len(keyParts) < 5 {
		err = goerrors.New("Instance status node key invalid: " + node.Key)
		return
	}

	keyParts = keyParts[2:]
	instance = new(Instance)

	instance.Group = keyParts[0]
	instance.Service = keyParts[1]
	instance.Instance, err = strconv.Atoi(keyParts[2])
	if err != nil {
		return
	}

	// Do not attempt to parse value if it is not present.
	if len(node.Value) > 0 {
		err = json.Unmarshal([]byte(node.Value), instance)
		if err != nil {
			return
		}
	}
	return
}

// Parses an instance from an update response and returns the instance.
func parseInstanceUpdate(update *etcd.Response) (instanceUpdate *InstanceUpdate, err error) {
	instanceUpdate = new(InstanceUpdate)

	instanceUpdate.Operation, err = parseActionToOperation(update.Action)
	if err != nil {
		return
	}

	instanceUpdate.Instance, err = parseInstance(update.Node)
	return
}
