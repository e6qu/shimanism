// mock subcommand wiring.
//
// `shim mock` bundles inmem-backed cloud-shaped HTTP frontends for
// every shimmed service on per-service ports. End users invoke it
// to stand up a fake universe of cloud APIs they can point their
// SDK / CLI / Terraform provider at for local development — same
// role the harness plays in conformance tests, packaged as a long-
// lived binary.
//
// Each frontend serves the source cloud's wire shape; the
// underlying state-of-record is the per-service inmem backend.
// Per the no-fakes rule, the frontends are exactly the production
// code (not mocks) — inmem is a real test fixture, not a stub.
//
// The default port layout matches the per-service shim defaults
// shifted by +1000 to avoid clashing with a real `shim <service>`
// run on the same host:
//
//	storage    :19000   (vs shim storage :9000)
//	secrets    :19100
//	queue      :19200
//	pubsub     :19300
//	rdbms      :19500
//	cache      :19600
//	functions  :19600 (… wait, conflict — actually :19700)
//	apigateway :19800
//
// Each port serves one (frontend, service) pair; `-frontend=<cloud>`
// selects which cloud's wire shape the mock universe emits.
//
// Example:
//
//	shim mock -frontend=aws
//	# in another shell:
//	eval "$(shimctl env --frontend=aws --service=storage --endpoint=http://localhost:19000)"
//	aws s3 ls
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"

	apigatewayinmem "github.com/e6qu/shimanism/services/apigateway/backends/inmem"
	cacheinmem "github.com/e6qu/shimanism/services/cache/backends/inmem"
	functionsinmem "github.com/e6qu/shimanism/services/functions/backends/inmem"
	pubsubinmem "github.com/e6qu/shimanism/services/pubsub/backends/inmem"
	queueinmem "github.com/e6qu/shimanism/services/queue/backends/inmem"
	rdbmsinmem "github.com/e6qu/shimanism/services/rdbms/backends/inmem"
	secretsinmem "github.com/e6qu/shimanism/services/secrets/backends/inmem"
	storageinmem "github.com/e6qu/shimanism/services/storage/backends/inmem"

	awsapigwfront "github.com/e6qu/shimanism/internal/apigateway/frontends/aws_apigatewayv2"
	awsecfront "github.com/e6qu/shimanism/internal/cache/frontends/aws_elasticache"
	awslambdafront "github.com/e6qu/shimanism/internal/functions/frontends/aws_lambda"
	awssnsfront "github.com/e6qu/shimanism/internal/pubsub/frontends/aws_sns"
	awssqsfront "github.com/e6qu/shimanism/internal/queue/frontends/aws_sqs"
	awsrdsfront "github.com/e6qu/shimanism/internal/rdbms/frontends/aws_rds"
	"github.com/e6qu/shimanism/internal/restxml"
	awssmfront "github.com/e6qu/shimanism/internal/secrets/frontends/aws_secretsmanager"
	awsfront "github.com/e6qu/shimanism/internal/storage/frontends/aws_s3"
	storagegen "github.com/e6qu/shimanism/services/storage/gen"
)

func runMock(args []string) error {
	fs := flag.NewFlagSet("mock", flag.ContinueOnError)
	frontend := fs.String("frontend", "aws",
		"frontend wire-shape family: aws (only AWS supported in initial mock matrix)")
	host := fs.String("host", "localhost", "bind host")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *frontend != "aws" {
		return fmt.Errorf("only -frontend=aws is supported in the initial mock matrix (GCP/Azure frontends require TLS / OAuth bring-up)")
	}

	listeners := []struct {
		Service string
		Port    int
		Handler http.Handler
	}{
		{Service: "storage", Port: 19000, Handler: awsStorageMock()},
		{Service: "secrets", Port: 19100, Handler: awssmfront.New(secretsinmem.New())},
		{Service: "queue", Port: 19200, Handler: awssqsfront.New(queueinmem.New())},
		{Service: "pubsub", Port: 19300, Handler: awssnsfront.New(pubsubinmem.New())},
		{Service: "rdbms", Port: 19500, Handler: awsrdsfront.New(rdbmsinmem.New())},
		{Service: "cache", Port: 19600, Handler: awsecfront.New(cacheinmem.New())},
		{Service: "functions", Port: 19700, Handler: awslambdafront.New(functionsinmem.New())},
		{Service: "apigateway", Port: 19800, Handler: awsapigwfront.New(apigatewayinmem.New())},
	}

	fmt.Fprintln(os.Stderr, "shim mock — inmem-backed cloud-shaped APIs:")
	var wg sync.WaitGroup
	for _, l := range listeners {
		addr := fmt.Sprintf("%s:%d", *host, l.Port)
		fmt.Fprintf(os.Stderr, "  %-12s %s  (frontend=%s)\n", l.Service, "http://"+addr, *frontend)
		wg.Add(1)
		go func(addr string, h http.Handler, svc string) {
			defer wg.Done()
			srv := &http.Server{Addr: addr, Handler: h}
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "shim mock %s: %v\n", svc, err)
			}
		}(addr, l.Handler, l.Service)
	}

	hint := "Use `shimctl env --frontend=" + *frontend + " --service=<svc> --endpoint=http://" + *host + ":<port>` to wire your tooling."
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, hint)
	fmt.Fprintln(os.Stderr, "Ctrl-C to stop.")

	wg.Wait()
	return nil
}

func awsStorageMock() http.Handler {
	router := &restxml.Router{}
	storagegen.RegisterAmazonS3Routes(router, awsfront.New(storageinmem.New()))
	return router
}
