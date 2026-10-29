//go:build ignore

// Synthetic constant response: isolate the existing proxy from SQL and fanout.
package main

import (
 "log"
 "net/http"
)

func main() {
 http.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
  w.Header().Set("Content-Type", "application/json")
  w.Write([]byte(`{"ok":true,"synthetic_only":true}`))
 })
 log.Fatal(http.ListenAndServe(":8080", nil))
}
