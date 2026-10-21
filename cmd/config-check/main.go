// config-check performs an offline preflight; it never opens database/network connections.
package main

import (
	"flag"
	"fmt"
	"os"

	"across/backend/internal/buildinfo"
	"across/backend/internal/config"
	"encoding/json"
)

func main() {
	role := flag.String("role", "api", "api, worker or migrate")
	expected := flag.String("expect-env", "production", "required APP_ENV")
	identity := flag.Bool("build-info", false, "print build identity only; do not load application configuration")
	flag.Parse()
	if *identity {
		_ = json.NewEncoder(os.Stdout).Encode(buildinfo.Snapshot("config-check"))
		return
	}
	cfg := config.Load()
	if cfg.AppEnv != *expected {
		fmt.Fprintln(os.Stderr, "configuration rejected: APP_ENV does not match --expect-env")
		os.Exit(1)
	}
	if err := cfg.Validate(*role); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("offline configuration preflight passed; connectivity, proxy topology and firewall still require verification")
}
