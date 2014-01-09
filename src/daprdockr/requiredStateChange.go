package main

import (
	"github.com/coreos/go-etcd/etcd"
	"strconv"
)

type RequiredStateChange struct {
	ServiceConfig *ServiceConfig
	Operation     Operation
	Instance      int
}

func RequiredStateChanges(client *etcd.Client, stop chan bool, errors *chan error) (changes chan map[string]*RequiredStateChange) {
	stopInstances := make(chan bool)
	stopServiceConfigs := make(chan bool)

	currentInstances := CurrentInstances(client, stopInstances, errors)
	currentServiceConfigs := CurrentServiceConfigs(client, stopServiceConfigs, errors)

	changes = make(chan map[string]*RequiredStateChange)
	go func() {

		desired := make(map[string]*ServiceConfig)
		current := make(map[string]*Instance)

	loop:
		for {
			// Wait for a state change or exit condition.
			select {
			case newServiceConfigs, ok := <-currentServiceConfigs:
				if !ok {
					break loop
				}
				desired = newServiceConfigs
			case newInstances, ok := <-currentInstances:
				if !ok {
					break loop
				}
				current = newInstances
			case _, _ = <-stop:
				break loop
			}

			// Find the delta between desired and current state.
			delta := make(map[string]*RequiredStateChange)

			// Check for additions and modifications.
			for _, serviceConfig := range desired {
				for i := 0; i < serviceConfig.Instances; i++ {
					key := strconv.Itoa(i) + "." + serviceConfig.QualifiedName()
					if _ /*instance*/, exists := current[key]; !exists {
						change := new(RequiredStateChange)
						change.ServiceConfig = serviceConfig
						change.Instance = i
						change.Operation = Add
						delta[key] = change
					} else {
						// Check that instance matches the service config - easiest thing to do is delete the instance
						// and wait for it to be re-added. Ensure good monitoring for equality issues.
					}
				}
			}

			// Check for deletions.
			for _, instance := range current {
				key := instance.QualifiedName()
				if _ /*serviceConfig*/, exists := desired[key]; !exists {
					// This instance must be deleted.
					change := new(RequiredStateChange)
					change.Instance = instance.Instance
					change.Operation = Remove
					delta[key] = change
				}
			}

			changes <- delta
		}

		stopInstances <- true
		stopServiceConfigs <- true
		close(stopInstances)
		close(stopServiceConfigs)
		close(stop)
		close(changes)
		return
	}()
	return
}
