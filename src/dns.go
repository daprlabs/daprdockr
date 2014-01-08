// DNS proxy server.
package main

import (
	"fmt"
	"github.com/miekg/dns"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
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
		fmt.Printf("Error! %s", err)
	}
	externalDns := new(dns.Client)

	return func(writer dns.ResponseWriter, request *dns.Msg) {

		for _, server := range resolvConf.Servers {
			response, _, err := externalDns.Exchange(request, server+":"+resolvConf.Port)
			if err != nil && debug {
				fmt.Printf("Error handling request: %s\n", err)
				continue
			}

			if debug {
				fmt.Printf("Default handler response: %s\n", response.String())
			}
			writer.WriteMsg(response)
			break
		}
	}
}

// Creates a handler for container domain requests.
func createContainerHandler() (handler func(dns.ResponseWriter, *dns.Msg)) {
	return func(writer dns.ResponseWriter, request *dns.Msg) {

		if debug {
			fmt.Printf("Request: %s\n", request.String())
		}

		var (
			ipv4            bool
			resourceRecord  dns.RR
			txtResponse     string
			responseAddress net.IP
		)
		// TC must be done here
		response := new(dns.Msg)
		response.SetReply(request)
		response.Compress = compress
		if ip, ok := writer.RemoteAddr().(*net.UDPAddr); ok {
			txtResponse = "Port: " + strconv.Itoa(ip.Port) + " (udp)"
			responseAddress = ip.IP
			ipv4 = responseAddress.To4() != nil
		}
		if ip, ok := writer.RemoteAddr().(*net.TCPAddr); ok {
			txtResponse = "Port: " + strconv.Itoa(ip.Port) + " (tcp)"
			responseAddress = ip.IP
			ipv4 = responseAddress.To4() != nil
		}

		name := request.Question[0].Name

		// Create the resouce record.
		if ipv4 {
			resourceRecord = new(dns.A)
			resourceRecord.(*dns.A).Hdr = dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0}
			resourceRecord.(*dns.A).A = responseAddress.To4()
		} else {
			resourceRecord = new(dns.AAAA)
			resourceRecord.(*dns.AAAA).Hdr = dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 0}
			resourceRecord.(*dns.AAAA).AAAA = responseAddress
		}

		// Tack on a TXT record
		txtRecord := new(dns.TXT)
		txtRecord.Hdr = dns.RR_Header{Name: "1234." + name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0}
		txtRecord.Txt = []string{txtResponse}

		switch request.Question[0].Qtype {
		case dns.TypeTXT:
			response.Answer = append(response.Answer, txtRecord)
			response.Extra = append(response.Extra, resourceRecord)
		default:
			fallthrough
		case dns.TypeAAAA, dns.TypeA:
			response.Answer = append(response.Answer, resourceRecord)
			response.Extra = append(response.Extra, txtRecord)
		}

		if request.IsTsig() != nil {
			if writer.TsigStatus() == nil {
				response.SetTsig(request.Extra[len(request.Extra)-1].(*dns.TSIG).Hdr.Name, dns.HmacMD5, 300, time.Now().Unix())
			} else if debug {
				println("Status", writer.TsigStatus().Error())
			}
		}
		if debug {
			fmt.Printf("%v\n", response.String())
		}
		writer.WriteMsg(response)
	}
}

func serve(net string) {
	server := &dns.Server{Addr: ":53", Net: net}
	err := server.ListenAndServe()
	if err != nil {
		fmt.Printf("Failed to setup the "+net+" server: %s\n", err.Error())
	}
}

func Start(containerDomainSuffix string) {
	dns.HandleFunc(containerDomainSuffix+".", createContainerHandler())
	dns.HandleFunc(".", createDefaultHandler())
	go serve("tcp")
	go serve("udp")
	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
forever:
	for {
		select {
		case s := <-sig:
			fmt.Printf("Signal (%d) received, stopping\n", s)
			break forever
		}
	}
}
