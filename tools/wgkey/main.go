// prints a wireguard keypair as json for scripting
package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/masaleem-oss/Fengard/internal/vpn"
)

func main() {
	priv, pub, err := vpn.NewKey()
	if err != nil {
		log.Fatal(err)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]string{"privateKey": priv, "publicKey": pub})
}
