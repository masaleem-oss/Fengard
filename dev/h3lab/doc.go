// Package h3lab runs fengard-h3 against quic-go, a separate http/3 implementation, through a
// udp proxy that loses, reorders, duplicates and delays packets. It's its own module so quic-go
// never ends up in fengard's go.mod.
package h3lab
