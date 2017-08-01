package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"github.com/alexflint/go-arg"
	"github.com/montanaflynn/stats"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

type Args struct {
	KeyFile     string `arg:"-k,required"`
	CertFile    string `arg:"-c,required"`
	Iterations  int    `arg:"-i,required"`
	Parallelism int    `arg:"-p,required"`
	Hostname    string `arg:"-H,required"`
	Port        int    `arg:"-P,required"`
}

type durations []time.Duration

type request struct {
	tlsconfig *tls.Config
	hostname  string
	port      int
}

type result struct {
	success bool
	err     error
	elapsed time.Duration
}

func (Args) Version() string {
	return os.Args[0] + " cosignperf 0.1"
}

func main() {
	var args Args
	arg.MustParse(&args)

	// load our key and cert
	clientcert, err := tls.LoadX509KeyPair(args.CertFile, args.KeyFile)
	if err != nil {
		log.Fatalf("%s\n", err)
	}

	// create tls config
	tlsconfig := &tls.Config{
		InsecureSkipVerify: false,
		ServerName:         args.Hostname,
		Certificates:       []tls.Certificate{clientcert},
	}

	requestc := make(chan request, args.Iterations)
	resultc := make(chan result, args.Iterations)

	// create workers
	for i := 1; i <= args.Parallelism; i++ {
		go worker(i, requestc, resultc)
	}

	// submit jobs
	start := time.Now()
	for i := 1; i <= args.Iterations; i++ {
		requestc <- request{tlsconfig: tlsconfig, hostname: args.Hostname, port: int(args.Port)}
	}

	// collect results
	var s durations
	var f durations
	var errors = make(map[string]int)
	for i := 1; i <= args.Iterations; i++ {
		r := <-resultc
		if r.success {
			s = append(s, r.elapsed)
		} else {
			f = append(f, r.elapsed)
			//errors[r.err.Error()]++
		}
	}
	elapsed := time.Since(start)

	var error_report string
	for e, i := range errors {
		error_report += fmt.Sprintf("%d\t%s\n", i, e)
	}

	fmt.Printf("\n===========\n"+
		"Total elapsed time: %s\n"+
		"Average req/s: %.2f\n"+
		"Parallelism: %d, SUCCESS/FAIL: %d/%d\n"+
		"SUCCESS: avg: %s, max: %s, min: %s, 99pct: %s, 95pct: %s\n"+
		"FAIL: avg: %s, max: %s, min: %s, 99pct: %s, 95pct: %s\n"+
		"Errors:\n%s",
		elapsed,
		float64(args.Iterations)/elapsed.Seconds(),
		args.Parallelism, len(s), len(f),
		s.dstat(stats.Mean), s.dstat(stats.Max), s.dstat(stats.Min), s.dpct(stats.Percentile, 99), s.dpct(stats.Percentile, 95),
		f.dstat(stats.Mean), f.dstat(stats.Max), f.dstat(stats.Min), f.dpct(stats.Percentile, 99), f.dpct(stats.Percentile, 95),
		error_report,
	)

}

func (d durations) dstat(f func(stats.Float64Data) (float64, error)) time.Duration {
	dfloat := make([]float64, len(d))
	for i, v := range d {
		dfloat[i] = float64(v)
	}

	s, err := f(dfloat)
	if err != nil {
		return 0
	} else {
		return time.Duration(s)
	}
}

func (d durations) dpct(f func(stats.Float64Data, float64) (float64, error), p float64) time.Duration {
	dfloat := make([]float64, len(d))
	for i, v := range d {
		dfloat[i] = float64(v)
	}

	s, err := f(dfloat, p)
	if err != nil {
		return 0
	} else {
		return time.Duration(s)
	}
}

func worker(w int, requestc <-chan request, resultc chan<- result) {
	for r := range requestc {
		success := false
		status := "SUCCESS"

		start := time.Now()

		// connect
		conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", r.hostname, r.port))
		if err != nil {
			status = fmt.Sprintf("NOCONN %s", err)
			success = false

		} else {
			message, _ := bufio.NewReader(conn).ReadString('\n')
			if strings.HasPrefix(message, "220 ") {
				status = fmt.Sprintf("SUCCESS %s", message)
				success = true

				// ask to STARTTLS
				conn.Write([]byte("STARTTLS 2\r\n"))
				message, _ = bufio.NewReader(conn).ReadString('\n')
				if strings.HasPrefix(message, "220 ") {
					// create new tls Conn and do tls handshake
					tlsconn := tls.Client(conn, r.tlsconfig)
					err = tlsconn.Handshake()
					if err == nil {
						// send NOOP command
						tlsconn.Write([]byte("NOOP\r\n"))
						message, _ = bufio.NewReader(conn).ReadString('\n')
						if strings.HasPrefix(message, "220 ") {
							status = fmt.Sprintf("NOOP SUCCESS %s", message)
							success = true
						} else {
							status = fmt.Sprint("NOOP FAIL %s", message)
							success = false
						}
					} else {
						status = fmt.Sprintf("STARTTLS FAIL %s", err)
						success = false
					}
				} else {
					status = fmt.Sprintf("STARTTLS FAIL %s", message)
					success = false
				}
			} else {
				status = fmt.Sprintf("BADRESPONSE %s", message)
				success = false
			}
		}

		conn.Close()
		elapsed := time.Since(start)
		log.Printf("%s %s", elapsed, status)

		resultc <- result{
			success: success,
			err:     err,
			elapsed: elapsed,
		}
	}
}
