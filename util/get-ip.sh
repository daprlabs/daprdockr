#!/bin/sh
usage() {
	echo <<DELIM "Echo public IP of a running service.
Usage:
	$0 {instance}.{service}.{group} [nameserver]
Example:
	$0 2.web.gulaghypercloud"
DELIM
}

[[ $# -lt 1 ]] && usage && exit
[[ ${1} == '-h' ]] && usage && exit

INSTANCE=${1} # Instance must be specified, in the form '<instance>.<service>.<group>'.
NAMESERVER=${2+'@'$2} # Use default nameserver if not specified.

dig ${NAMESERVER} ${INSTANCE}.container +short A