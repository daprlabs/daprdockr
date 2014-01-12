// DNS proxy server.
package daprdockr

import (
	"errors"
	"github.com/miekg/dns"
	"log"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	debugDnsServer        = false
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
		question := request.Question[0]
		containerDomainSuffixLen := len(ContainerDomainSuffix) + 2
		name := question.Name

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
		getInstanceForARecord := func(name string) (instance *Instance, err error) {
			instanceName := name[:len(name)-containerDomainSuffixLen]
			instance, ok := instances[instanceName]
			if !ok {
				err = errors.New("Could not find entry for " + name)
				return nil, err
			}
			return
		}

		getIpAddrs := func(addrs []string) (result []net.IP) {
			result = make([]net.IP, 0, len(addrs))
			for _, addr := range addrs {
				result = append(result, net.ParseIP(addr))
			}
			return
		}

		addr4 := func(addrs []string) (result net.IP, err error) {
			for _, addr := range getIpAddrs(addrs) {
				converted := addr.To4()
				if converted != nil {
					result = converted
					return
				}
			}

			err = errors.New("Unable to find an IPv4 address.")
			return
		}

		addr6 := func(addrs []string) (result net.IP, err error) {
			for _, addr := range getIpAddrs(addrs) {
				converted := addr.To16()
				if converted != nil {
					result = converted
					return
				}
			}

			err = errors.New("Unable to find an IPv6 address.")
			return
		}

		tryARecord := func(addrs []string, name string) (record dns.RR) {
			if address, err := addr4(addrs); err == nil {
				record = new(dns.A)
				record.(*dns.A).Hdr = dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0}
				record.(*dns.A).A = address
			} else if errorChan != nil {
				*errorChan <- err
			}
			return
		}

		tryAAAARecord := func(addrs []string, name string) (record dns.RR) {
			if address, err := addr6(addrs); err == nil {
				record = new(dns.AAAA)
				record.(*dns.AAAA).Hdr = dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 0}
				record.(*dns.AAAA).AAAA = address
			} else if errorChan != nil {
				*errorChan <- err
			}
			return
		}

		createSRVRecord := func(question, service, target string) (record dns.RR, err error) {
			record = new(dns.SRV)
			record.(*dns.SRV).Hdr = dns.RR_Header{Name: question, Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: 0}
			record.(*dns.SRV).Target = target
			targetPort, err := strconv.ParseUint(service, 10, 16)
			record.(*dns.SRV).Port = uint16(targetPort)
			if err != nil {
				return
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
		case dns.TypeSRV:
			parts := strings.SplitN(name[:len(name)-containerDomainSuffixLen], ".", 3)
			if len(parts) == 3 {
				privatePort := parts[0]
				//protocol := parts[1] // Ignored
				instanceName := parts[2]
				if instance, ok := instances[instanceName]; ok {

					if publicPort, ok := instance.PortMappings[privatePort]; ok {
						target := strings.SplitN(name, ".", 3)[2]
						answer, err := createSRVRecord(name, publicPort, target)
						if err == nil {
							response.Answer = append(response.Answer, answer)
						}
					}
				}
			}
		/*case dns.TypeTXT:
		response.Answer = append(response.Answer, txtRecord)
		*/
		default:
			fallthrough
		case dns.TypeA:
			instance, err := getInstanceForARecord(name)
			if err == nil {
				answer := tryARecord(instance.Addrs, name)
				if answer == nil {
					answer = tryAAAARecord(instance.Addrs, name)
				}
				if answer != nil {
					response.Answer = append(response.Answer, answer)
				}
				//response.Extra = append(response.Extra, txtRecord)
			}
		case dns.TypeAAAA:
			instance, err := getInstanceForARecord(name)
			if err == nil {
				answer := tryAAAARecord(instance.Addrs, name)
				if answer == nil {
					answer = tryARecord(instance.Addrs, name)
				}
				if answer != nil {

					response.Answer = append(response.Answer, answer)
				}
				//response.Extra = append(response.Extra, txtRecord)
			}
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
