module github.com/masaleem-oss/Fengard/dev/h3lab

go 1.27.0

replace github.com/masaleem-oss/Fengard => ../..

require (
	github.com/masaleem-oss/Fengard v0.0.0-00010101000000-000000000000
	github.com/miekg/dns v1.1.73
	github.com/quic-go/quic-go v0.63.0
)

require (
	github.com/quic-go/qpack v0.6.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)
