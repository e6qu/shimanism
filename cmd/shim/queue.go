// Queue subcommand wiring. Mirrors the storage + secrets subcommand
// shape: pick a frontend (wire protocol) and a backend (destination),
// listen on -addr, serve forever.

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	natsapi "github.com/nats-io/nats.go"
	"google.golang.org/api/option"
	pubsubraw "google.golang.org/api/pubsub/v1"

	"github.com/e6qu/shimanism/internal/queue/domain"
	awssqsfront "github.com/e6qu/shimanism/internal/queue/frontends/aws_sqs"
	azuresbfront "github.com/e6qu/shimanism/internal/queue/frontends/azure_servicebus"
	gcpsubfront "github.com/e6qu/shimanism/internal/queue/frontends/gcp_pubsub"
	awsqbackend "github.com/e6qu/shimanism/services/queue/backends/aws"
	azureqbackend "github.com/e6qu/shimanism/services/queue/backends/azure"
	gcpqbackend "github.com/e6qu/shimanism/services/queue/backends/gcp"
	"github.com/e6qu/shimanism/services/queue/backends/inmem"
	natsqbackend "github.com/e6qu/shimanism/services/queue/backends/nats"
)

func runQueue(args []string) error {
	fs := flag.NewFlagSet("queue", flag.ContinueOnError)
	addr := fs.String("addr", ":9200", "address to listen on")
	frontendName := fs.String("frontend", "aws_sqs",
		"frontend wire protocol: aws_sqs, gcp_pubsub, azure_servicebus")
	backendName := fs.String("backend", "inmem",
		"backend: inmem, nats, aws, gcp, azure")

	// Backend connection knobs.
	natsURL := fs.String("nats-url", envOr("NATS_URL", ""), "NATS JetStream URL (e.g. nats://localhost:4222)")
	awsEndpoint := fs.String("aws-endpoint", envOr("AWS_SQS_ENDPOINT", ""),
		"AWS SQS endpoint override (empty = default)")
	gcpProject := fs.String("gcp-project", envOr("GCP_PROJECT_ID", ""),
		"GCP project ID (for the gcp backend)")
	azureConn := fs.String("azure-connection-string",
		envOr("AZURE_SERVICEBUS_CONNECTION_STRING", ""),
		"Azure Service Bus connection string")

	if err := fs.Parse(args); err != nil {
		return err
	}

	backend, err := buildQueueBackend(*backendName, queueBackendConfig{
		natsURL:     *natsURL,
		awsEndpoint: *awsEndpoint,
		gcpProject:  *gcpProject,
		azureConn:   *azureConn,
	})
	if err != nil {
		return err
	}

	handler, err := buildQueueFrontend(*frontendName, backend)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "shim queue: frontend=%s backend=%s addr=%s\n",
		*frontendName, *backendName, *addr)
	return http.ListenAndServe(*addr, handler)
}

type queueBackendConfig struct {
	natsURL     string
	awsEndpoint string
	gcpProject  string
	azureConn   string
}

func buildQueueBackend(name string, cfg queueBackendConfig) (domain.Queues, error) {
	switch name {
	case "inmem":
		return inmem.New(), nil
	case "nats":
		if cfg.natsURL == "" {
			return nil, fmt.Errorf("nats backend requires -nats-url (or NATS_URL)")
		}
		nc, err := natsapi.Connect(cfg.natsURL)
		if err != nil {
			return nil, fmt.Errorf("connect to NATS: %w", err)
		}
		return natsqbackend.New(nc)
	case "aws":
		awsConf, err := awscfg.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		opts := []func(*awssqs.Options){}
		if cfg.awsEndpoint != "" {
			opts = append(opts, func(o *awssqs.Options) {
				o.BaseEndpoint = awsapi.String(cfg.awsEndpoint)
			})
		}
		return awsqbackend.New(awssqs.NewFromConfig(awsConf, opts...)), nil
	case "gcp":
		if cfg.gcpProject == "" {
			return nil, fmt.Errorf("gcp backend requires -gcp-project (or GCP_PROJECT_ID)")
		}
		svc, err := pubsubraw.NewService(context.Background(),
			option.WithUserAgent("shimanism-queue"))
		if err != nil {
			return nil, fmt.Errorf("connect to GCP Pub/Sub: %w", err)
		}
		return gcpqbackend.New(svc, gcpqbackend.Config{ProjectID: cfg.gcpProject}), nil
	case "azure":
		if cfg.azureConn == "" {
			return nil, fmt.Errorf("azure backend requires -azure-connection-string (or AZURE_SERVICEBUS_CONNECTION_STRING)")
		}
		return azureqbackend.New(azureqbackend.Config{ConnectionString: cfg.azureConn})
	default:
		return nil, fmt.Errorf("unknown backend %q (valid: inmem, nats, aws, gcp, azure)", name)
	}
}

func buildQueueFrontend(name string, backend domain.Queues) (http.Handler, error) {
	switch name {
	case "aws_sqs":
		return awssqsfront.New(backend), nil
	case "gcp_pubsub":
		return gcpsubfront.New(backend), nil
	case "azure_servicebus":
		return azuresbfront.New(backend), nil
	default:
		return nil, fmt.Errorf("unknown frontend %q (valid: aws_sqs, gcp_pubsub, azure_servicebus)", name)
	}
}
