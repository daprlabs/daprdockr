#!/bin/sh
usage() {
	echo <<DELIM "Echo public port of a running service.
Usage:
	$0 {instance}.{service}.{group} {internal port} [nameserver]
Example:
	$0 2.web.gulaghypercloud 8080"
DELIM
}

[[ $# -lt 2 ]] && usage && exit
[[ ${1} -eq '-h' ]] && usage && exit

INSTANCE=${1} # Instance must be specified, in the form '<instance>.<service>.<group>'.
PORT=${2} # Port must be specified
NAMESERVER=${3+'@'$3} # Use default nameserver if not specified.

dig ${NAMESERVER} ${PORT}.tcp.${INSTANCE}.container +short SRV | cut -d" " -f3