// DNS proxy server.
package main

import (
	"errors"
	"fmt"
	"github.com/miekg/dns"
	"net"
	"os"
	"time"
)

const (
	debug    = false
	compress = false
)

// Creates a handler which proxies requests via the host system's configured DNS servers.
func createDefaultHandler() (handler func(dns.ResponseWriter, *dns.Msg)) {
	resolvConf, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error! %s", err)
	}
	externalDns := new(dns.Client)

	return func(writer dns.ResponseWriter, request *dns.Msg) {

		for _, server := range resolvConf.Servers {
			response, _, err := externalDns.Exchange(request, server+":"+resolvConf.Port)
			if err != nil && debug {
				fmt.Fprintf(os.Stderr, "Error handling request: %s\n", err)
				continue
			}

			if debug {
				fmt.Fprintf(os.Stderr, "Default handler response: %s\n", response.String())
			}
			writer.WriteMsg(response)
			break
		}
	}
}

// Creates a handler for container domain requests.
func createContainerHandler(currentInstances chan map[string]*Instance) (handler func(dns.ResponseWriter, *dns.Msg)) {
	var instances *map[string]*Instance
	go func() {
		for current := range currentInstances {
			instances = &current
		}
	}()

	return func(writer dns.ResponseWriter, request *dns.Msg) {
		//var txtResponse       string
		var responseAddresses []net.IP

		if debug {
			fmt.Fprintf(os.Stderr, "Request: %s\n", request.String())
		}

		fmt.Fprintf(os.Stderr, "Instances %+v", instances)

		instances := *instances
		question := request.Question[0]
		name := question.Name

		response := new(dns.Msg)
		response.SetReply(request)
		response.Compress = compress

		// Check that we have a record matching the request
		if instance, ok := instances[name[0:len(name)-1]]; ok {
			fmt.Fprintf(os.Stderr, "Found instance! %s", instance.FullyQualifiedDomainName())
			responseAddresses = instance.Addrs
		} else {
			fmt.Fprintf(os.Stderr, "\nCould not find instance for %s\n\n", name)
		}

		addr4 := func(addrs []net.IP) (result net.IP, err error) {
			for _, addr := range addrs {
				converted := addr.To4()
				fmt.Fprintf(os.Stderr, "addr4: Trying %s - %b", addr, converted != nil)
				if converted != nil {
					result = converted
					return
				}
			}

			err = errors.New("Unable to find an IPv4 address.")
			return
		}

		addr6 := func(addrs []net.IP) (result net.IP, err error) {
			for _, addr := range addrs {
				converted := addr.To16()
				fmt.Fprintf(os.Stderr, "addr6: Trying %s - %b", addr, converted != nil)
				if converted != nil {
					result = converted
					return
				}
			}

			err = errors.New("Unable to find an IPv6 address.")
			return
		}

		tryARecord := func(addrs []net.IP, name string) (record dns.RR) {
			if address, err := addr4(responseAddresses); err == nil {
				record = new(dns.A)
				record.(*dns.A).Hdr = dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0}
				record.(*dns.A).A = address
			}
			return
		}

		tryAAAARecord := func(addrs []net.IP, name string) (record dns.RR) {
			if address, err := addr6(responseAddresses); err == nil {
				record = new(dns.AAAA)
				record.(*dns.AAAA).Hdr = dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 0}
				record.(*dns.AAAA).AAAA = address
			}
			return
		}

		/*
			// Tack on a TXT record
			txtRecord := new(dns.TXT)
			txtRecord.Hdr = dns.RR_Header{Name: "1234." + name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0}
			txtRecord.Txt = []string{txtResponse}
		*/

		switch question.Qtype {
		/*case dns.TypeTXT:
		response.Answer = append(response.Answer, txtRecord)
		*/
		default:
			fallthrough
		case dns.TypeA:
			answer := tryARecord(responseAddresses, name)
			if answer == nil {
				answer = tryAAAARecord(responseAddresses, name)
			}
			if answer != nil {

				response.Answer = append(response.Answer, answer)
			}
			//response.Extra = append(response.Extra, txtRecord)
		case dns.TypeAAAA:
			answer := tryAAAARecord(responseAddresses, name)
			if answer == nil {
				answer = tryARecord(responseAddresses, name)
			}
			if answer != nil {

				response.Answer = append(response.Answer, answer)
			}
			//response.Extra = append(response.Extra, txtRecord)
		}

		if request.IsTsig() != nil {
			if writer.TsigStatus() == nil {
				response.SetTsig(request.Extra[len(request.Extra)-1].(*dns.TSIG).Hdr.Name, dns.HmacMD5, 300, time.Now().Unix())
			} else if debug {
				println("Status", writer.TsigStatus().Error())
			}
		}
		if debug {
			fmt.Fprintf(os.Stderr, "%v\n", response.String())
		}
		writer.WriteMsg(response)
	}
}

func serve(net string) {
	server := &dns.Server{Addr: ":53", Net: net}
	err := server.ListenAndServe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to setup the "+net+" server: %s\n", err.Error())
	}
}

func ServeDNS(containerDomainSuffix string, currentInstances chan map[string]*Instance) {
	dns.HandleFunc(containerDomainSuffix+".", createContainerHandler(currentInstances))
	dns.HandleFunc(".", createDefaultHandler())
	go serve("tcp")
	go serve("udp")
}