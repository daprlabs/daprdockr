package main

import (
	goerrors "errors"
	"log"
	"time"
)

var RequiredStateChangeRetry = time.Second * 15

type RequiredStateChange struct {
	ServiceConfig *ServiceConfig
	Operation     Operation
	Instance      int
}

type ServiceState struct {
	Service   *ServiceConfig
	Instances []*Instance
}

func RequiredStateChanges(instances chan map[string]*Instance, serviceConfigs chan map[string]*ServiceConfig, stop chan bool, errors *chan error) (changes chan map[string]*RequiredStateChange) {
	changes = make(chan map[string]*RequiredStateChange, 10)
	go func() {
		defer close(changes)
		desired := make(map[string]*ServiceConfig)
		current := make(map[string]*Instance)

		for {
			// Wait for a state change or exit condition.
			select {
			case newServiceConfigs, ok := <-serviceConfigs:
				if !ok {
					break
				}
				desired = newServiceConfigs
			case newInstances, ok := <-instances:
				if !ok {
					break
				}
				current = newInstances
			case _, _ = <-stop:
				break
			case _ = <-time.After(RequiredStateChangeRetry):
			}

			// Find the delta between desired and current state.
			delta := make(map[string]*RequiredStateChange)

			// Check for additions and modifications.
			for _, serviceConfig := range desired {
				for i := 0; i < serviceConfig.Instances; i++ {
					key := serviceConfig.InstanceQualifiedName(i)
					if _ /*instance*/, exists := current[key]; !exists {
						change := new(RequiredStateChange)
						change.ServiceConfig = serviceConfig
						change.Instance = i
						change.Operation = Add
						delta[key] = change
						log.Printf("[WorkFinder] Need to %s %s.\n", change.Operation.String(), key)
					} else {
						// ToDo: Check that instance matches the service config - easiest thing to do is delete the instance
						// and wait for it to be re-added. Ensure good monitoring for equality issues.
					}
				}
			}

			// Check for deletions.
			for _, instance := range current {
				key := instance.Service + "." + instance.Group
				if _ /*serviceConfig*/, exists := desired[key]; !exists {
					// This instance must be deleted.
					change := new(RequiredStateChange)
					change.Instance = instance.Instance
					change.Operation = Remove
					delta[key] = change
					log.Printf("[WorkFinder] Need to %s %d.%s.\n", change.Operation.String(), change.Instance, key)
				}
			}

			if len(delta) > 0 {
				changes <- delta
			} else {
				log.Println("[WorkFinder] All services seem healthy.")
			}
		}
		if errors != nil {
			*errors <- goerrors.New("Exiting RequiredStateChanges")
		}
	}()
	return
}
