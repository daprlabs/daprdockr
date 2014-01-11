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
	UpdateTimeToLive         = 10 // Seconds
	LockTimeToLive           = 60 // Seconds
	FullInstanceSyncInterval = 60 // Seconds
)

type instances struct {
	Heartbeats chan *Instance
	Flatlines  chan *Instance
}

var Instances = &instances{}

func init() {
	Instances.Heartbeats = make(chan *Instance)
	Instances.Flatlines = make(chan *Instance)
}

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
	Heartbeat // Instance is alive
	Flatline  // Instance has died
)

func (op Operation) String() (result string) {
	switch op {
	case Add:
		result = "Add"
	case Remove:
		result = "Remove"
	case Heartbeat:
		result = "Heartbeat"
	case Flatline:
		result = "Flatline"
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

func updateInstanceInStore(client *etcd.Client, instance *Instance) (err error) {
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

func removeInstanceFromStore(client *etcd.Client, instance *Instance) (err error) {
	_, err = client.Delete(instancePath(instance.Group, instance.Service, instance.Instance), false)
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

// Broadcasts the latest instance updates to the output channels.
// Multiple subsequent messages to any given channel are suppressed and only the latest value is made available for consumers.
func LatestInstances(etcdClient *etcd.Client, stop chan bool, numChans int, throttleInterval time.Duration) (outgoing []chan map[string]*Instance) {
	incomingUpdates := currentInstances(etcdClient, stop)
	return latestInstanceUpdates(incomingUpdates, numChans, throttleInterval)
}

// Broadcasts messages from the provided channel to the output channels.
// Multiple subsequent messages to any given channel are suppressed and only the latest value is made available for consumers.
func latestInstanceUpdates(incomingUpdates chan map[string]*Instance, numChans int, throttleInterval time.Duration) (outgoing []chan map[string]*Instance) {
	outgoing = make([]chan map[string]*Instance, 0, numChans)
	incoming := make([]chan map[string]*Instance, 0, numChans)
	for i := 0; i < numChans; i++ {
		incoming = append(incoming, make(chan map[string]*Instance))
		outgoing = append(outgoing, latestInstances(incoming[i], throttleInterval))
	}
	go func() {
		for instances := range incomingUpdates {
			for _, ch := range incoming {
				select {
				case ch <- instances:
				default:
				}
			}
		}
	}()
	return
}

// Forwards messages from the provided channel to the output channel.
// Multiple subsequent messages are suppressed and only the latest value is made available for consumers.
func latestInstances(incomingUpdates chan map[string]*Instance, throttleInterval time.Duration) chan map[string]*Instance {
	// Consumer channel.
	consumer := make(chan map[string]*Instance)

	// Shared channels.
	updateAvailable := make(chan bool, 1)
	latestUpdate := make(chan map[string]*Instance)

	// Get updates as they are made available.
	go func() {
		var latest map[string]*Instance
		for {
			select {
			case latestUpdate <- latest:
				// The latest update has been retrieved.
			case latest = <-incomingUpdates:
				// A new update has arrived.
				select {
				case updateAvailable <- true:
				// Notify of an update, if not already notified.
				default:
				}
			}
		}
	}()

	// Provide only the latest update to the consumer.
	go func() {
		throttler := time.NewTicker(throttleInterval)

		// Wait for initial update to be made available.
		<-updateAvailable
		for {
			// Retrieve update.
			update := <-latestUpdate

			// Either wait for another update (and retrieve it), or pass the latest update to the consumer.
			select {
			case <-updateAvailable:
				// Another update became available before the previous one was consumed.
			case consumer <- update:
				// The latest update has been consumed.
				// Wait for throttler.
				<-throttler.C
				// Wait for another update.
				<-updateAvailable
			}
		}
	}()
	return consumer
}

// Returns a channel publishing the current instances whenever they change.
func currentInstances(client *etcd.Client, stop chan bool) (currentInstances chan map[string]*Instance) {
	currentInstancesMap := make(map[string]*Instance)

	updated := func(update *InstanceUpdate) (instances map[string]*Instance, changed bool) {
		instances = currentInstancesMap
		name := update.Instance.QualifiedName()
		switch update.Operation {
		case Heartbeat:
			log.Printf("[Instances] Heartbeat %s.\n", name)
			err := updateInstanceInStore(client, update.Instance)
			if err != nil {
				log.Printf("[Instances] Failed to update store with heartbeat for %s.\n", name)
			}
			fallthrough
		case Add:
			if current, exists := currentInstancesMap[name]; !exists || !current.Equals(update.Instance) {
				currentInstancesMap[name] = update.Instance
				changed = true
				log.Printf("[Instances] Adding %s.\n", name)
			}
		case Flatline:
			log.Printf("[Instances] Flatline %s.\n", name)
			err := removeInstanceFromStore(client, update.Instance)
			if err != nil {
				log.Printf("[Instances] Failed to update store with demise of %s.\n", name)
			}
			fallthrough
		case Remove:
			if _, exists := currentInstancesMap[name]; exists {
				delete(currentInstancesMap, name)
				changed = true
				log.Printf("[Instances] Removing %s.\n", name)
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
	log.Printf("[Instances] Pulling all instances.\n")
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
	log.Printf("[Instances] Pulled instances.\n")
}

// Returns a channel of all instance updates.
func instanceUpdates(client *etcd.Client, stop chan bool) (updates chan *InstanceUpdate) {
	updates = make(chan *InstanceUpdate)

	go func() {
		getAllInstances(client, updates)
		incomingUpdates := make(chan *etcd.Response)
		go func() {
			for {
				select {
				case <-stop:
					break
				default:
				}
				client.Watch("instances", 0, true, incomingUpdates, stop)
			}
		}()
		fullSync := time.NewTicker(FullInstanceSyncInterval * time.Second)
		for {
			select {
			case <-stop:
				break
			case <-fullSync.C:
				getAllInstances(client, updates)
			case incomingUpdate := <-incomingUpdates:
				instance, err := parseInstanceUpdate(incomingUpdate)
				if err != nil {
					log.Printf("[Instances] Unable to parse instance update: %s.\n", err)
				} else {
					updates <- instance
				}
			case instance := <-Instances.Heartbeats:
				update := new(InstanceUpdate)
				update.Instance = instance
				update.Operation = Heartbeat
				updates <- update
			case instance := <-Instances.Flatlines:
				update := new(InstanceUpdate)
				update.Instance = instance
				update.Operation = Flatline
				updates <- update
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
		err = errors.New("Attempted to parse lock node")
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

	if err != nil {
		instanceUpdate = nil
	} else {
		instanceUpdate.Instance = instance
	}
	return
}
