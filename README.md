=== DO NOT USE THIS JUST YET ==
It's currently very much just for experimentation.

DaprDocker is an agent for hosting declaratively described services in a cluster.
The system stores both desired and current state in etcd. DaprDocker agents on each host read desired state and race to
satisfy it, using etcd as a distributed lock manager. Heartbeats from agent machines keep the current state up-to-date.

State change hooks allow an internal DNS proxy server to point to individual instances of a service within the cluster,
using the special .container top-level domain.
For example, 1.web.myapp.container will point to instance 1 of the "web" service in the "myapp" group.

State change hooks also allow an Nginx server to load balance HTTP requests over the healthy containers in a Web
service.