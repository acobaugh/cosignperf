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
	KeyFile       string `arg:"-k,required"`
	CertFile      string `arg:"-c,required"`
	Iterations    int    `arg:"-i,required,help:# of commands to issue per thread"`
	Threads       int    `arg:"-t,required,help:# of threads/clients to create"`
	Hostname      string `arg:"-H,required"`
	Port          int    `arg:"-P,required"`
	Command       string `arg:"-C,required:cosign command to issue"`
	SslSkipVerify bool   `arg:"help:Disable SSL verification when doing STARTTLS"`
}

type durations []time.Duration

type request struct {
	tlsconfig  *tls.Config
	hostname   string
	port       int
	command    string
	iterations int
}

type result struct {
	success bool
	status  string
	elapsed time.Duration
}

func (Args) Version() string {
	return os.Args[0] + " cosignperf 0.1"
}

func main() {
	var args Args
	args.SslSkipVerify = false
	args.Port = 6663
	args.Hostname = "localhost"
	args.Command = "NOOP"
	arg.MustParse(&args)

	// load our key and cert
	clientcert, err := tls.LoadX509KeyPair(args.CertFile, args.KeyFile)
	if err != nil {
		log.Fatalf("%s\n", err)
	}

	// create tls config
	tlsconfig := &tls.Config{
		InsecureSkipVerify: args.SslSkipVerify,
		ServerName:         args.Hostname,
		Certificates:       []tls.Certificate{clientcert},
	}

	requestc := make(chan request, args.Threads)
	resultc := make(chan result, args.Threads*args.Iterations)

	// create workers
	for i := 1; i <= args.Threads; i++ {
		go worker(i, requestc, resultc)
	}

	// submit jobs
	start := time.Now()
	for i := 1; i <= args.Threads; i++ {
		requestc <- request{tlsconfig: tlsconfig, hostname: args.Hostname, port: int(args.Port), command: args.Command, iterations: args.Iterations}
	}

	// collect results
	var s durations
	var f durations
	var errors = make(map[string]int)
	for i := 1; i <= (args.Iterations * args.Threads); i++ {
		r := <-resultc
		if r.success {
			s = append(s, r.elapsed)
		} else {
			f = append(f, r.elapsed)
			errors[r.status]++
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
		"Threads: %d, Commands/thread: %d, SUCCESS/FAIL: %d/%d\n"+
		"SUCCESS: avg: %s, max: %s, min: %s, 99pct: %s, 95pct: %s\n"+
		"FAIL: avg: %s, max: %s, min: %s, 99pct: %s, 95pct: %s\n"+
		"Errors:\n%s",
		elapsed,
		float64(args.Iterations*args.Threads)/elapsed.Seconds(),
		args.Threads, args.Iterations, len(s), len(f),
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
				// ask to STARTTLS
				conn.Write([]byte("STARTTLS 2\r\n"))
				message, _ = bufio.NewReader(conn).ReadString('\n')
				if strings.HasPrefix(message, "220 ") {
					// create new tls Conn and do tls handshake
					tlsconn := tls.Client(conn, r.tlsconfig)
					err = tlsconn.Handshake()
					message, _ = bufio.NewReader(tlsconn).ReadString('\n') // need to read cosignd's response to the starttls
					if err == nil {
						for i := 1; i <= r.iterations; i++ {
							// send command
							tlsconn.Write([]byte(r.command + "\r\n"))
							message, _ = bufio.NewReader(tlsconn).ReadString('\n')
							resp := strings.SplitN(message, " ", 2)
							switch resp[0] {
							case "220", "231", "232", "533", "534", "431", "432", "250":
								status = fmt.Sprintf("SUCCESS %s", message)
								success = true
							default:
								status = fmt.Sprintf("FAILRESPONSE %s", message)
								success = false
							}
							// more commands to follow, so report our result
							elapsed := time.Since(start)
							log.Printf("[%d:%d] %s %s", w, i, elapsed, status)
							resultc <- result{
								success: success,
								status:  status,
								elapsed: elapsed,
							}
							start = time.Now()
						}
					} else {
						status = fmt.Sprintf("HANDSHAKE FAIL %s", err)
						success = false
					}
				} else {
					status = message
					success = false
				}
			} else {
				status = fmt.Sprintf("BADRESPONSE %s", message)
				success = false
			}
		}

		conn.Write([]byte("QUIT\r\n"))
		conn.Close()

		// FIXME: there has to be a more elegant way to handle errors
		if !success {
			elapsed := time.Since(start)
			log.Printf("[%d] %s %s", w, elapsed, status)

			resultc <- result{
				success: success,
				status:  status,
				elapsed: elapsed,
			}
		}
	}
}
