// ingress-lab is a disposable CI identity probe, not an application server.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"across/backend/internal/middleware"
	"github.com/gofiber/fiber/v2"
)

func main() {
	probe := flag.String("probe", "", "reserved fixture address only")
	xff := flag.String("xff", "", "synthetic forwarding fixture")
	cf := flag.String("cf", "", "synthetic Cloudflare header")
	flag.Parse()
	if os.Getenv("APP_ENV") != "test" || os.Getenv("ATL_INGRESS_LAB") != "1" {
		fmt.Fprintln(os.Stderr, "disposable ingress fixture refused outside explicit test mode")
		os.Exit(1)
	}
	if *probe != "" {
		allowed := map[string]bool{"10.244.89.2:8080": true, "10.244.89.3:8080": true, "10.244.89.4:8080": true}
		if !allowed[*probe] {
			os.Exit(1)
		}
		request, _ := http.NewRequest("GET", "http://"+*probe+"/api/v1/ready", nil)
		request.Host = "api.staging.invalid"
		if *xff != "" {
			request.Header.Set("X-Forwarded-For", *xff)
		}
		if *cf != "" {
			request.Header.Set("CF-Connecting-IP", *cf)
		}
		response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
		if err != nil {
			os.Exit(1)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			os.Exit(1)
		}
		var result map[string]string
		if json.NewDecoder(response.Body).Decode(&result) != nil {
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode(result)
		return
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	// These are fixtures in a CI-only internal Docker network, not production
	// proxy recommendations. Edge trust is explicit for the upstream case.
	app.Use(middleware.TrustedClientIP("10.244.89.2/32,10.244.89.3/32,10.244.89.7/32"))
	app.Get("/api/v1/ready", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"peer": c.Context().RemoteIP().String(), "verified": c.Locals("atl.verified-client-ip")})
	})
	if app.Listen(":8080") != nil {
		os.Exit(1)
	}
}
