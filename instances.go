package daprdockr

import (
	"encoding/json"
	"errors"
	goerrors "errors"
	"github.com/coreos/go-etcd/etcd"
	"log"
	"net"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const (
	UpdateTimeToLive         = 20 // Seconds
	LockTimeToLive           = 60 // Seconds
	FullInstanceSyncInterval = 60 // Seconds
)

type Instance struct {
	Group        string `json:"-"`
	Service      string `json:"-"`
	Instance     int    `json:"-"`
	Addrs        []net.IP
	PortMappings map[string]string // Map from host port to container port.
}

func (this *Instance) String() string {
	addrs := make([]string, 0)
	for _, addr := range this.Addrs {
		addrs = append(addrs, addr.String())
	}

	ports := make([]string, 0)
	for k, v := range this.PortMappings {
		ports = append(ports, k+":"+v)
	}

	return this.QualifiedName() + "@" + strings.Join(addrs, ",") + "{" + strings.Join(ports, ",") + "}"
}

func (this *Instance) Equals(other *Instance) (equal bool) {
	return reflect.DeepEqual(this, other)
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

func instancePath(group, service string, instance int) string {
	return "instances/" + group + "/" + service + "/" + strconv.Itoa(instance)
}

func UpdateInstance(client *etcd.Client, instance *Instance) (err error) {
	payload, err := json.Marshal(instance)
	if err != nil {
		return
	}
	_, err = client.Set(instancePath(instance.Group, instance.Service, instance.Instance), string(payload), UpdateTimeToLive)
	if err != nil {
		return
	}
	return
}

func LockInstance(client *etcd.Client, instance int, service *ServiceConfig) (err error) {
	key := instancePath(service.Group, service.Name, instance)
	_, err = client.Create(key, "", LockTimeToLive)
	return
}

// Returns a channel publishing the current instances whenever they change.
func CurrentInstances(client *etcd.Client, stop chan bool) (currentInstances chan map[string]*Instance) {
	currentInstancesMap := make(map[string]*Instance)

	updated := func(update *InstanceUpdate) (instances map[string]*Instance, changed bool) {
		instances = currentInstancesMap
		name := update.Instance.QualifiedName()
		switch update.Operation {
		case Add:
			if current, exists := currentInstancesMap[name]; !exists || !current.Equals(update.Instance) {
				currentInstancesMap[name] = update.Instance
				changed = true
				log.Printf("[Instances] Adding %s.\n", update.Instance)
			}
		case Remove:
			if _, exists := currentInstancesMap[name]; exists {
				delete(currentInstancesMap, name)
				changed = true
				log.Printf("[Instances] Removing %s.\n", update.Instance)
			}
		}
		return
	}

	currentInstances = make(chan map[string]*Instance, 10)
	go func() {
		defer close(currentInstances)
		for update := range instanceUpdates(client, stop) {
			// Mutate the current instances collection and publish it.
			newCurrentInstances, changed := updated(update)
			if changed {
				currentInstances <- newCurrentInstances
			}
		}

		log.Printf("[Instances] Exiting.\n")
	}()

	return
}

func getAllInstances(client *etcd.Client, instances chan *InstanceUpdate) {
	response, err := client.Get("instances", false, true)
	if err != nil {
		log.Printf("[Instances] Unable to get instances directory: %s.\n", err)
		return
	}

	for _, n := range response.Node.Nodes {
		for _, node := range n.Nodes {
			r, err := client.Get(node.Key, false, true)
			if err != nil {
				log.Printf("[Instances] Unable to get instance: %s.\n", err)
				continue
			}
			for _, iNode := range r.Node.Nodes {
				instance, err := parseInstance(&iNode)
				if err != nil {
					log.Printf("[Instances] Unable to parse instance: %s.\n", err)
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
func instanceUpdates(client *etcd.Client, stop chan bool) (updates chan *InstanceUpdate) {
	updates = make(chan *InstanceUpdate)
	incomingUpdates := make(chan *etcd.Response)

	go func() {
		watchChan := make(chan *etcd.Response)
		go func() {
			for {
				select {
				case <-stop:
					break
				default:
				}
				client.Watch("instances", 0, true, watchChan, stop)
			}
		}()
		getAllInstances(client, updates)
		for {
			select {
			case <-stop:
				break
			case <-time.After(FullInstanceSyncInterval * time.Second):
				getAllInstances(client, updates)
			case incomingUpdate := <-watchChan:
				incomingUpdates <- incomingUpdate
			}
		}
	}()
	go func() {
		defer close(incomingUpdates)
		for update := range incomingUpdates {
			instance, err := parseInstanceUpdate(update)
			if err != nil {
				log.Printf("[Instances] Unable to parse instance update: %s.\n", err)
				continue
			} else if instance != nil {
				updates <- instance
			}
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
	} else {
		// This is a lock node.
		// Note that this is not an error.
		instance = nil
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

	instance, err := parseInstance(update.Node)
	if instance == nil {
		instanceUpdate = nil
	} else {
		instanceUpdate.Instance = instance
	}
	return
}
