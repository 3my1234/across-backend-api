package main

import (
	"fmt"
	webpush "github.com/SherClockHolmes/webpush-go"
	"log"
)

func main() {
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		log.Fatal("Could not generate notification keys")
	}
	fmt.Printf("WEB_PUSH_VAPID_PUBLIC_KEY=%s\nWEB_PUSH_VAPID_PRIVATE_KEY=%s\nWEB_PUSH_SUBJECT=mailto:support@atlxpres.com\n", public, private)
}
