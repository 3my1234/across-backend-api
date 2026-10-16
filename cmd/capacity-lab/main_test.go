package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMetricsDoNotCountOverloadOrFastFailuresAsSuccess(t *testing.T) {
	m := &metric{}
	m.add(sample{Status: 200, Valid: true, Elapsed: 300 * time.Millisecond})
	m.add(sample{Status: 503, Elapsed: time.Millisecond})
	m.add(sample{Status: 500, Elapsed: time.Millisecond})
	m.add(sample{Status: 200, Elapsed: time.Millisecond}) // Invalid response body.
	m.finish()
	if m.Requests != 4 || m.Success != 1 || m.Busy != 1 || m.Errors != 2 || m.P95MS != 300 {
		t.Fatalf("incorrect success accounting: %+v", m)
	}
}
func TestLabHTTPClientDoesNotFollowRedirects(t *testing.T) {
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer source.Close()
	c := client()
	defer c.CloseIdleConnections()
	res, err := c.Get(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 302 || reached {
		t.Fatal("lab client followed a redirect outside its target")
	}
}
