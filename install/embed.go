// Package install carries the router files fengardd puts back by itself, so the copies on the
// router always match the program that's running
package install

import _ "embed"

// the services > fengard page in luci, the same files the ipk and apk packages ship
var (
	//go:embed luci/menu.json
	LuCIMenu []byte
	//go:embed luci/acl.json
	LuCIACL []byte
	//go:embed luci/fengard.js
	LuCIView []byte
	//go:embed luci/fengard-luci
	LuCIHelper []byte
)

// installed as /etc/fengard/uninstall.sh
//
//go:embed router-uninstall.sh
var Uninstall []byte
