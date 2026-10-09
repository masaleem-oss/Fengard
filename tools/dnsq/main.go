// dnsq server name
package main

import (
	"fmt"
	"os"

	"github.com/miekg/dns"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Println("usage: dnsq <server:port> <name>")
		os.Exit(2)
	}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(os.Args[2]), dns.TypeA)
	r, err := dns.Exchange(m, os.Args[1])
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	fmt.Printf("%-28s %s", os.Args[2], dns.RcodeToString[r.Rcode])
	for _, a := range r.Answer {
		if a, ok := a.(*dns.A); ok {
			fmt.Print(" ", a.A)
		}
	}
	fmt.Println()
}
