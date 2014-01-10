// DNS proxy server.
package main

import (
	"errors"
	"github.com/miekg/dns"
	"log"
	"net"
	"time"
)

const (
	debugDnsServer        = true
	compressDnsResponses  = false
	ContainerDomainSuffix = "container"
)

// Start a DNS server so that the addresses of service instances can be resolved.
func StartDnsServer(currentInstances chan map[string]*Instance, errorChan *chan error) {
	dns.HandleFunc(ContainerDomainSuffix+".", createContainerHandler(currentInstances, errorChan))
	dns.HandleFunc(".", createDefaultHandler(errorChan))
	go serve("tcp", errorChan)
	go serve("udp", errorChan)

	// TODO: When https://github.com/miekg/dns implements .Stop(), leverage that here.
}

func serve(net string, errorChan *chan error) {
	server := &dns.Server{Addr: ":53", Net: net}
	err := server.ListenAndServe()
	if err != nil && errorChan != nil {
		*errorChan <- err
	}
}

// Creates a handler which proxies requests via the host system's configured DNS servers.
func createDefaultHandler(errorChan *chan error) (handler func(dns.ResponseWriter, *dns.Msg)) {
	resolvConf, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		if errorChan != nil {
			*errorChan <- err
		}
	}
	externalDns := new(dns.Client)

	return func(writer dns.ResponseWriter, request *dns.Msg) {

		for _, server := range resolvConf.Servers {
			response, _, err := externalDns.Exchange(request, server+":"+resolvConf.Port)
			if err != nil {
				if debugDnsServer {
					log.Printf("Error handling request: %s.\n", err)
				}
				if errorChan != nil {
					*errorChan <- err
				}
				continue
			}

			if debugDnsServer {
				log.Printf("Default handler response: %s.\n", response.String())
			}
			writer.WriteMsg(response)
			break
		}
	}
}

// Creates a handler for container domain requests.
func createContainerHandler(currentInstances chan map[string]*Instance, errorChan *chan error) (handler func(dns.ResponseWriter, *dns.Msg)) {
	var instances *map[string]*Instance
	go func() {
		for current := range currentInstances {
			log.Printf("[DNS] Updating hosts. Hosts: %d.\n", len(current))
			instances = &current
		}
	}()

	return func(writer dns.ResponseWriter, request *dns.Msg) {
		//var txtResponse       string
		var responseAddresses []net.IP
		question := request.Question[0]
		suffixLen := len(ContainerDomainSuffix) + 2
		name := question.Name
		instanceName := name[:len(name)-suffixLen]

		response := new(dns.Msg)
		response.SetReply(request)
		response.Compress = compressDnsResponses

		if instances == nil {
			writer.WriteMsg(response)
			err := errors.New("DNS query made but instances is nil.")
			if errorChan != nil {
				*errorChan <- err
			}
			return
		}

		instances := *instances

		// Check that we have a record matching the request
		if instance, ok := instances[instanceName]; ok {
			responseAddresses = instance.Addrs
		} else if debugDnsServer {
			log.Printf("[DNS] Could not find entry for %s.\n", name)
			writer.WriteMsg(response)
			return
		}

		addr4 := func(addrs []net.IP) (result net.IP, err error) {
			for _, addr := range addrs {
				converted := addr.To4()
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
				if converted != nil {
					result = converted
					return
				}
			}

			err = errors.New("Unable to find an IPv6 address.")
			return
		}

		tryARecord := func(addrs []net.IP, name string) (record dns.RR) {
			if address, err := addr4(addrs); err == nil {
				record = new(dns.A)
				record.(*dns.A).Hdr = dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0}
				record.(*dns.A).A = address
			} else if errorChan != nil {
				*errorChan <- err
			}
			return
		}

		tryAAAARecord := func(addrs []net.IP, name string) (record dns.RR) {
			if address, err := addr6(addrs); err == nil {
				record = new(dns.AAAA)
				record.(*dns.AAAA).Hdr = dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 0}
				record.(*dns.AAAA).AAAA = address
			} else if errorChan != nil {
				*errorChan <- err
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
			} else if debugDnsServer {
				log.Printf("[DNS] TsigStatus", writer.TsigStatus().Error())
			}
		}
		if debugDnsServer {
			log.Printf("[DNS] Replying to query for %s with:\n%v.\n", name, response.String())
		}
		writer.WriteMsg(response)
	}
}
