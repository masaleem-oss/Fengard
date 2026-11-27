package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/masaleem-oss/Fengard/internal/routerui"
	"github.com/masaleem-oss/Fengard/internal/web"
)

// a firmware upgrade or a restored backup keeps fengard running but drops its page in luci,
// so it goes back in place here. only on a router install, a computer has no luci
func restoreRouterPage() {
	if _, err := os.Stat(installEnv); err != nil {
		return
	}
	if _, err := os.Stat("/etc/openwrt_release"); err != nil {
		return
	}
	h := routerui.Healer{Root: "/", Version: version, Logo: web.Logo(), Reload: reloadRPCD}
	res, err := h.Ensure()
	if err != nil {
		log.Printf("luci page: %v", err)
	}
	if len(res.Page) > 0 {
		log.Printf("put Fengard's page back in the router's web interface (Services > Fengard): %s", strings.Join(res.Page, " "))
	}
	if res.Uninstaller {
		log.Printf("brought /etc/fengard/uninstall.sh up to date with this version")
	}
}

func reloadRPCD() error {
	if _, err := os.Stat("/etc/init.d/rpcd"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/etc/init.d/rpcd", "reload").Run()
}
