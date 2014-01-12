DaprDockr
=========

DaprDocker is an agent for hosting declaratively described services in a cluster.
The system stores both desired and current state in etcd. DaprDocker agents on each host read desired state and race to
satisfy it, using etcd as a distributed lock manager. Heartbeats from agent machines keep the current state up-to-date.

An internal DNS proxy server point to individual instances of a service within the cluster,
using the special _.container_ top-level domain.
For example, 1.web.myapp.container will point to instance 1 of the "web" service in the "myapp" group.

DaprDockr manages child Nginx processes to provide containers with HTTP load balancing and reverse proxy support. To leverage this feature, the service configuration must specify an HTTP hostname (eg: gulaghypercloud.com) and the HTTP port inside the container (eg: "9000" for Play! Framework).

<a href="http://imgur.com/WWgbdq0"><img src="http://i.imgur.com/WWgbdq0.png" title="Some DaprDockr daemon output" /></a>

Features
--------
- Simple commandline interface for controlling a cluster of docker + etcd nodes.
- `daprdockrd` agents on each cluster node race to satisfy the service configurations stored in etcd by starting or stopping containers.
- HTTP Load Balancer / Reverse Proxy support for containers via Nginx.
- DNS Server for looking up the current location of a container.
- DNS Server handles SRV records for discovering port mappings at runtime.

Planned
-------
- Agents should load balance containers across all nodes, rather than being greedy.

Usage
-----

*NOTE:* The easiest way to use this is via the [daprlabs/daprdockr](https://github.com/daprlabs/docker-daprdockrd/) container on a [CoreOS](http://coreos.com/) box. See the link for run info. The container is prebuilt on the [Docker public index](https://index.docker.io/u/daprlabs/docker-daprdockrd/).

### Daemon ###
`daprdockrd -help`:
```
Usage of ./daprdockrd:
  -cpuprofile="": write cpu profile to file
  -docker="unix:///var/run/docker.sock": URLs of the local docker instance.
  -etcd="http://localhost:5001,http://localhost:5002,http://localhost:5003": Comma separated list of URLs of the cluster's etcd.
```

### Utility ###
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

#### Example

Given a service definition in a file, say `service.json`:
```javascript
{
  "Name": "web",
  "Group": "service",
  "Instances": 5,
  "Container": {
    "Image": "daprlabs/testwebapp"
    },
  "Http": {
    "HostName": "service.com",
    "ContainerPort": "80"
  }
}


```
You can instantiate that service using `daprdockrcmd`:
```
$ ./daprdockrcmd -set -stdin < service.json
{
  "Name": "web",
  "Group": "service",
  "Instances": 5,
  "Container": {
    "Hostname": "",
    "Domainname": "",
    "User": "",
    "Memory": 0,
    "MemorySwap": 0,
    "CpuShares": 0,
    "AttachStdin": false,
    "AttachStdout": false,
    "AttachStderr": false,
    "PortSpecs": null,
    "ExposedPorts": null,
    "Tty": false,
    "OpenStdin": false,
    "StdinOnce": false,
    "Env": null,
    "Cmd": null,
    "Dns": null,
    "Image": "daprlabs/testwebapp",
    "Volumes": null,
    "VolumesFrom": "",
    "WorkingDir": "",
    "Entrypoint": null,
    "NetworkDisabled": false
  },
  "Http": {
    "HostName": "service.com",
    "ContainerPort": "80"
  }
}
```

Assuming that _service.com_ is pointed at your docker hosts (`/etc/hosts` helps for testing), you can watch `daprdockrd` as it spins up your containers and configures DNS and the HTTP Load Balancer (Nginx).

### Querying containers via DNS
Two basics forms of query, both leverage the special `.container` pseudo-top-level-domain:

1. `<instance>.<service>.<group>.container` for A (IPv4 address) and [AAAA](http://en.wikipedia.org/wiki/IPv6_address#IPv6_addresses_in_the_Domain_Name_System) (IPv6 address) queries.
  * This can be used to find the IP of the host the container is running on.
2. `<private port>.<protocol>.<instance>.<service>.<group>.container` for [SRV](http://en.wikipedia.org/wiki/SRV_record) queries.
  * This can be used for discovering port mappings. Currently, _protocol_ is ignored.


#### Query container IP

```
# Note that @localhost is used as the nameserver in this example, since it was run
# directly on the daprdockrd instance. If it were run inside the container, @localhost
# should be omitted.
$ dig @localhost 1.web.service.container

; <<>> DiG 9.9.2-P2 <<>> @localhost 1.web.service.container
; (2 servers found)
;; global options: +cmd
;; Got answer:
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 34170
;; flags: qr rd; QUERY: 1, ANSWER: 1, AUTHORITY: 0, ADDITIONAL: 0
;; WARNING: recursion requested but not available

;; QUESTION SECTION:
;1.web.service.container.	IN	A

;; ANSWER SECTION:
1.web.service.container. 0	IN	A	192.168.1.10

;; Query time: 0 msec
;; SERVER: ::1#53(::1)
;; WHEN: Sat Jan 11 20:58:03 2014
;; MSG SIZE  rcvd: 80
```

For convenience, [`get-ip.sh`](/util/get-ip.sh), is included for outputting just the public port.
```
# Note that when testing from outside a managed container,
# the address of the daprdockr daemon must be provided as the nameserver.
$ ./get-ip.sh 1.web.service
192.168.1.10
```

#### Query container port mappings.
Below, we can see that port 80 inside the container is mapped to port 49169 on the host.
```
$ dig @localhost 80.tcp.1.web.service.container SRV
; <<>> DiG 9.9.2-P2 <<>> @localhost 80.tcp.1.web.service.container SRV
; (2 servers found)
;; global options: +cmd
;; Got answer:
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 51248
;; flags: qr rd; QUERY: 1, ANSWER: 1, AUTHORITY: 0, ADDITIONAL: 0
;; WARNING: recursion requested but not available

;; QUESTION SECTION:
;80.tcp.1.web.service.container.	IN	SRV

;; ANSWER SECTION:
80.tcp.1.web.service.container.	0 IN	SRV	0 0 49169 1.web.service.

;; Query time: 1 msec
;; SERVER: ::1#53(::1)
;; WHEN: Sat Jan 11 20:50:23 2014
;; MSG SIZE  rcvd: 111

```

For convenience, [`get-port.sh`](/util/get-port.sh), is included for outputting just the mapped port.
```
# Note that when testing from outside a managed container,
# the address of the daprdockr daemon must be provided as the nameserver.
$ ./get-port.sh 1.web.service 80
49175
```
For additional convenience, [`get-ip-port.sh`](/util/get-ip-port.sh) is included for outputting the ip:port combo.
```
# Note that when testing from outside a managed container,
# the address of the daprdockr daemon must be provided as the nameserver.
$ ./get-port.sh 1.web.service 80
192.168.1.10:49175
```

Requirements
------------

Each cluster note must have network access to:
- etcd

Each cluster node must have:
- docker
- nginx
