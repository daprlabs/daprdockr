// TODO: understand how IPv6 routing works...
package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io/ioutil"
	"net"
	"strings"
)

const (
	Route4FilePath = "/proc/net/route"
	Route6FilePath = "/proc/net/ipv6_route"
)

var internetDestintationIPv4 = []byte{0, 0, 0, 0}
var internetDestintationIPv6 = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

/*
type Flags uint

const (
	Up Flags = iota
	Host
	Gateway
	Reinstate
	Dynamic
	Modified
	Addrconf
	Cache
	Reject
)
*/

// Returns a collection of IP addresses which have a route to the Internet
func InternetRoutedIPs() (ips []net.IP, err error) {
	routes, err := parseRoutes()
	if err != nil {
		return
	}

	internetConnectedInterfaces := getInternetRoutes(routes)
	allIps := make(map[string]net.IP)
	for _, route := range internetConnectedInterfaces {
		newIps, err := getRouteAddresses(route)
		if err != nil {
			return ips, err
		}
		for _, ip := range newIps {
			allIps[ip.String()] = ip
		}
	}

	ips = make([]net.IP, 0, len(allIps))
	for _, ip := range allIps {
		ips = append(ips, ip)
	}
	return
}

func getRouteAddresses(route RouteEntry) (ips []net.IP, err error) {
	addrs, err := route.Iface.Addrs()
	ips = make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		if ipNet.Contains(route.Gateway) {
			ips = append(ips, ipNet.IP)
		}
	}
	return
}

// Filters out non-Internet-facing routes from input routes.
func getInternetRoutes(routes []RouteEntry) (inetRoutes []RouteEntry) {
	inetRoutes = make([]RouteEntry, 0, 2)
	for _, route := range routes {
		if bytes.Equal(route.Destination, internetDestintationIPv4) || bytes.Equal(route.Destination, internetDestintationIPv6) {
			inetRoutes = append(inetRoutes, route)
		}

	}
	return
}

type RouteEntry struct {
	Destination net.IP
	Gateway     net.IP
	Genmask     net.IPMask
	//Flags       uint
	//Metric      uint
	//Ref   uint
	//Use   uint
	Iface net.Interface
}

func getInterfaces() (interfaces map[string]net.Interface, err error) {
	interfaces = make(map[string]net.Interface)
	allInterfaces, err := net.Interfaces()
	if err != nil {
		return
	}

	for _, this := range allInterfaces {
		interfaces[this.Name] = this
	}
	return
}
func parseRoutes() (routes []RouteEntry, err error) {

	interfaces, err := getInterfaces()
	if err != nil {
		return
	}
	routes4, err := parseRoutes4(interfaces)
	if err != nil {
		return
	} /*
		routes6, err := parseRoutes6(interfaces)
		if err != nil {
			return
		}*/

	routes = routes4 /*append(routes4, routes6...)*/
	return
}

func parseRoutes4(interfaces map[string]net.Interface) (routes []RouteEntry, err error) {
	routes = make([]RouteEntry, 0, 10)
	systemRouteText, err := ioutil.ReadFile(Route4FilePath)
	if err != nil {
		return
	}
	systemRouteLines := strings.Split(string(systemRouteText), "\n")
	for _, routeText := range systemRouteLines[1:] {
		if len(routeText) < 2 {
			continue
		}
		routeEntry, err := parseRoute4(routeText, interfaces)
		if err != nil {
			return nil, err
		}
		routes = append(routes, routeEntry)
	}

	return
}
func parseRoute4(routeText string, interfaces map[string]net.Interface) (route RouteEntry, err error) {
	columns := strings.Fields(routeText)
	systemInterface, ok := interfaces[columns[0]]
	if !ok {
		err = errors.New("Unknown interface: \"" + columns[0] + "\"")
		return
	}
	route.Iface = systemInterface

	route.Destination, err = parseIP(columns[1])
	if err != nil {
		return route, err
	}

	route.Gateway, err = parseIP(columns[2])
	if err != nil {
		return route, err
	}

	mask, err := parseIP(columns[3])
	if err != nil {
		return route, err
	}
	route.Genmask = net.IPMask(mask)
	return
}

/*
func parseRoutes6(interfaces map[string]net.Interface) (routes []RouteEntry, err error) {
	routes = make([]RouteEntry, 0, 10)
	systemRouteText, err := ioutil.ReadFile(Route6FilePath)
	if err != nil {
		return
	}
	systemRouteLines := strings.Split(string(systemRouteText), "\n")
	for _, routeText := range systemRouteLines {
		if len(routeText) < 2 {
			continue
		}
		routeEntry, err := parseRoute6(routeText, interfaces)
		if err != nil {
			return nil, err
		}
		routes = append(routes, routeEntry)
	}

	return
}
func parseRoute6(routeText string, interfaces map[string]net.Interface) (route RouteEntry, err error) {
	columns := strings.Fields(routeText)
	if len(columns) < 10 {
		err = errors.New("Insufficient columnns in route entry")
		return
	}
	systemInterface, ok := interfaces[columns[9]]
	if !ok {
		err = errors.New("Unknown interface: \"" + columns[9] + "\"")
		return
	}
	route.Iface = systemInterface

	route.Destination, err = parseIP(columns[0])
	if err != nil {
		return route, err
	}

	route.Gateway, err = parseIP(columns[3])
	if err != nil {
		return route, err
	}

	mask, err := parseIP(columns[3])
	if err != nil {
		return route, err
	}
	route.Genmask = net.IPMask(mask)
	return
}*/

func parseIP(hexBytes string) (ip net.IP, err error) {

	ipBytes, err := hex.DecodeString(hexBytes)
	if err != nil {
		return ip, err
	}
	reverseByteSlice(ipBytes)
	ip = net.IP(ipBytes)
	return
}

func reverseByteSlice(slice []byte) {
	for i, j := 0, len(slice)-1; i < j; i, j = i+1, j-1 {
		slice[i], slice[j] = slice[j], slice[i]
	}
}
