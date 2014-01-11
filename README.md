DaprDockr
=========

DaprDocker is an agent for hosting declaratively described services in a cluster.
The system stores both desired and current state in etcd. DaprDocker agents on each host read desired state and race to
satisfy it, using etcd as a distributed lock manager. Heartbeats from agent machines keep the current state up-to-date.

An internal DNS proxy server point to individual instances of a service within the cluster,
using the special _.container_ top-level domain.
For example, 1.web.myapp.container will point to instance 1 of the "web" service in the "myapp" group.

DaprDockr manages child Nginx processes to provide containers with HTTP load balancing and reverse proxy support. To leverage this feature, the service configuration must specify an HTTP hostname (eg: gulaghypercloud.com) and the HTTP port inside the container (eg: "9000" for Play! Framework).

Features
--------
- Simple commandline interface for controlling a cluster of docker + etcd nodes.
- `daprdockrd` agents on each cluster node race to satisfy the service configurations stored in etcd by starting or stopping containers.
- HTTP Load Balancer / Reverse Proxy support for containers via Nginx.
- DNS Server for looking up the current location of a container.

Planned
-------
- Agents should load balance containers across all nodes, rather than being greedy.
- DNS SRV records for discovering port mappings at runtime.

Usage
-----
### Daemon ###
`daprdockrd -help`:
```
Usage of ./daprdockrd:
  -cpuprofile="": write cpu profile to file
  -docker="unix:///var/run/docker.sock": URLs of the local docker instance.
  -etcd="http://localhost:5001,http://localhost:5002,http://localhost:5003": Comma separated list of URLs of the cluster's etcd.
```

### Commandline Interface ###
`daprdockrcmd -help`
```
Usage of ./daprdockrcmd:
  -cmd="": The command to run in the container.
  -del=false: Delete service configuration.
  -etcd="http://localhost:5001,http://localhost:5002,http://localhost:5003": Comma separated list of URLs of the cluster's etcd.
  -get=true: Get service configuration.
  -http-host="": The HTTP hostname used for load balancing this service.
  -http-port="": The HTTP port within the container for load balancing.
  -image="": The service image in the form accepted by docker.
  -instances=0: The target number of service instances.
  -set=false: Set service configuration.
  -stdin=false: Read JSON service definition from stdin.
  -svc="": The service to operate on, in the form "<service>.<group>".
  -v=false: Provide verbose output.
```

Requirements
------------
Each cluster note must have network access to:
- etcd
- 
Each cluster node must have:
- docker
- nginx
