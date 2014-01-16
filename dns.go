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
	debugDnsServer           = false
	compressDnsResponses     = false
	ContainerDomainSuffix    = "container"
	ContainerDomainSuffixLen = len(ContainerDomainSuffix) + 2
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
		for _, question := range request.Question {
			for _, instance := range getInstancesFromQuestion(question, instances) {
				switch question.Qtype {
				case dns.TypeSRV:
					parts := strings.SplitN(question.Name, ".", 3)
					if len(parts) >= 3 {
						// Extract the service, which is the container port.
						containerPort := parts[0]
						// Determine the host port which the service is mapped to.
						if hostPortStr, ok := instance.PortMappings[containerPort]; ok {
							hostPort, err := strconv.ParseUint(hostPortStr, 10, 16)
							if err == nil {
								target := parts[2]
								// Construct the response.
								response.Answer = append(response.Answer, &dns.SRV{
									Hdr:    dns.RR_Header{Name: question.Name, Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: 0},
									Target: target,
									Port:   uint16(hostPort),
								})
							}
						}
					}
				case dns.TypeA, dns.TypeAAAA:
					for _, addr := range instance.Addrs {
						ip := net.ParseIP(addr)
						switch {
						case ip == nil:
							continue
						case ip.To4() != nil:
							response.Answer = append(response.Answer, &dns.A{
								Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0},
								A:   ip.To4(),
							})
						case ip.To16() != nil:
							response.Answer = append(response.Answer, &dns.AAAA{
								Hdr:  dns.RR_Header{Name: question.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 0},
								AAAA: ip.To16(),
							})
						}
					}
				}
			}
		}

		if request.IsTsig() != nil {
			if writer.TsigStatus() == nil {
				response.SetTsig(request.Extra[len(request.Extra)-1].(*dns.TSIG).Hdr.Name, dns.HmacMD5, 300, time.Now().Unix())
			} else if debugDnsServer {
				log.Printf("[DNS] TsigStatus", writer.TsigStatus().Error())
			}
		}
		writer.WriteMsg(response)
	}
}

// TODO: Use a more optimal data structure for these kinds of operations.
func getInstancesFromQuestion(question dns.Question, allInstances map[string]*Instance) (instances []*Instance) {
	questionSansSentinel := question.Name[:len(question.Name)-ContainerDomainSuffixLen]
	instances = make([]*Instance, 0, 4)
	switch strings.Count(questionSansSentinel, ".") {
	case 3: // <instance>.<service>.<group>.container
		if instance, exists := allInstances[questionSansSentinel]; exists {
			instances = append(instances, instance)
		}
	case 2: // <service>.<group>.container
		// See TODO above. This is obviously a poor way to find matching instances.
		for name, instance := range allInstances {
			nameParts := strings.SplitN(name, ".", 2)
			if len(nameParts) == 2 && nameParts[1] == question.Name {
				instances = append(instances, instance)
			}
		}
	case 1: // <group>.container
		// See TODO above. This is obviously a poor way to find matching instances.
		for name, instance := range allInstances {
			nameParts := strings.SplitN(name, ".", 3)
			if len(nameParts) == 3 && nameParts[2] == question.Name {
				instances = append(instances, instance)
			}
		}
	default:
	}

	return
}
