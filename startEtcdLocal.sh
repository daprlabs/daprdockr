#!/bin/sh
export PUBLIC_IP=192.168.1.10
docker run -d -p 8001:8001 -p 5001:5001 -p 4001:4001 coreos/etcd -peer-addr ${PUBLIC_IP}:8001 -addr ${PUBLIC_IP}:5001 -name etcd-node1
docker run -d -p 8002:8002 -p 5002:5002 -p 4003:4001 coreos/etcd -peer-addr ${PUBLIC_IP}:8002 -addr ${PUBLIC_IP}:5002 -name etcd-node2 -peers ${PUBLIC_IP}:8001,${PUBLIC_IP}:8002,${PUBLIC_IP}:8003
docker run -d -p 8003:8003 -p 5003:5003 -p 4002:4001 coreos/etcd -peer-addr ${PUBLIC_IP}:8003 -addr ${PUBLIC_IP}:5003 -name etcd-node3 -peers ${PUBLIC_IP}:8001,${PUBLIC_IP}:8002,${PUBLIC_IP}:8003