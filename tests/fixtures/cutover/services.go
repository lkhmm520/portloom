// Package main is a test-only driver; it is not a production cutover entry point.
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lkhmm520/portloom/internal/cutover"
)

const zone = "cutover.test"

var hosts = []string{"a." + zone, "b." + zone}
var nodes = []string{"source", "target"}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing fixture command")
	}
	switch args[0] {
	case "edge":
		if len(args) == 3 {
			return edge(args[1], args[2])
		}
	case "origin":
		if len(args) == 2 {
			return origin(args[1])
		}
	case "seed-dns":
		if len(args) == 3 {
			return seedDNS(args[1], args[2])
		}
	case "enroll", "prepare", "forward", "rollback":
		if len(args) == 1 {
			return drive(args[0])
		}
	}
	return fmt.Errorf("invalid fixture command")
}

func management(id string) string { return id + "." + zone }
func preview(id, host string) string {
	return strings.SplitN(host, ".", 2)[0] + "-preview-" + management(id)
}

func server(addr string, handler http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 3 * time.Second, WriteTimeout: 8 * time.Second, IdleTimeout: 8 * time.Second}
}

func edge(id, verifier string) error {
	proxy := func(port int) *httputil.ReverseProxy {
		return &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				r.Out.URL.Scheme = "http"
				r.Out.URL.Host = "127.0.0.1:" + strconv.Itoa(port)
				r.SetXForwarded()
			},
			Transport: &http.Transport{ResponseHeaderTimeout: 3 * time.Second},
		}
	}
	admin, gateway := proxy(8080), proxy(8081)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The test accepts only explicitly provisioned virtual hosts. Preview is
		// accessible only from the verifier container, not even from the Agents.
		if r.TLS.ServerName != r.Host {
			http.Error(w, "SNI/Host mismatch", http.StatusMisdirectedRequest)
			return
		}
		if r.Host == management(id) {
			admin.ServeHTTP(w, r)
			return
		}
		peer, _, _ := net.SplitHostPort(r.RemoteAddr)
		for _, host := range hosts {
			if r.Host == host || (r.Host == preview(id, host) && peer == verifier) {
				gateway.ServeHTTP(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})
	return server(":443", handler).ListenAndServeTLS("/fixture/leaf.pem", "/fixture/leaf.key")
}

type mapping struct {
	host, prefix, probe, originPath string
	port                            int
}

var mappings = []mapping{
	{hosts[0], "", "/a-canary", "/a-canary", 9001},
	{hosts[0], "/api", "/api/nested-canary", "/nested-canary", 9002},
	{hosts[1], "", "/b-canary", "/b-canary", 9001},
}

func body(id, path string) string { return id + ":" + path + "\n" }

func origin(id string) error {
	errs := make(chan error, 2)
	for _, port := range []int{9001, 9002} {
		go func() {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, m := range mappings {
					if port == m.port && r.URL.Path == m.originPath {
						// Deliberately wrong, duplicate origin claims. Only the Server
						// gateway may turn these into a single trusted node marker.
						w.Header().Add(cutover.HTTPSProbeNodeHeader, "not-the-edge")
						w.Header().Add(cutover.HTTPSProbeNodeHeader, "another-upstream-claim")
						w.Header().Add(cutover.HTTPSProbeRouteHeader, "not-the-route")
						w.Header().Add(cutover.HTTPSProbeRouteHeader, "another-upstream-route-claim")
						fmt.Fprint(w, body(id, m.originPath))
						return
					}
				}
				http.NotFound(w, r)
			})
			errs <- server("127.0.0.1:"+strconv.Itoa(port), handler).ListenAndServe()
		}()
	}
	return <-errs
}
