// Command echoserver runs the calibrated echo target used by Stampede's
// accuracy benchmark.
//
//	echoserver -addr :9099 -dist lognormal:20ms:0.5
//	curl localhost:9099/stats      # exact server-side percentiles
//	curl -X POST localhost:9099/reset
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/Ivan825/Stampede/bench/echo"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9099", "listen address")
	dist := flag.String("dist", "fixed:10ms", "delay distribution: fixed:D, uniform:LO:HI, lognormal:MEDIAN:SIGMA, bimodal:FAST:SLOW:FRACTION")
	seed := flag.Uint64("seed", 1, "random seed")
	flag.Parse()
	s, err := echo.New(*dist, *seed)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("echoserver listening on %s with %s", *addr, *dist)
	log.Fatal(http.ListenAndServe(*addr, s))
}
