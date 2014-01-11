#!/bin/sh
usage() {
	echo <<DELIM "Echo the address of a running service in the form IP:PORT.
Usage:
	$0 {instance}.{service}.{group} {internal port} [nameserver]
Example:
	$0 2.web.gulaghypercloud 8080"
DELIM
}

[[ $# -lt 2 ]] && usage && exit
[[ ${1} == '-h' ]] && usage && exit

INSTANCE=${1} # Instance must be specified, in the form '<instance>.<service>.<group>'.
PORT=${2} # Port must be specified
NAMESERVER=${3+'@'$3} # Use default nameserver if not specified.

PPORT=`dig ${NAMESERVER} ${PORT}.tcp.${INSTANCE}.container +short SRV | cut -d" " -f3`
IP=`dig ${NAMESERVER} ${INSTANCE}.container +short A`
echo $IP:$PPORT