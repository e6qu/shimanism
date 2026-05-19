// Pubsub subcommand wiring. The AWS frontend listens on two
// addresses (SNS publish + SQS-shaped receive); other frontends
// use a single -addr.

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	natsapi "github.com/nats-io/nats.go"
	"google.golang.org/api/option"
	pubsubraw "google.golang.org/api/pubsub/v1"

	"github.com/e6qu/shimanism/internal/pubsub/domain"
	awssnsfront "github.com/e6qu/shimanism/internal/pubsub/frontends/aws_sns"
	awssqsreceivefront "github.com/e6qu/shimanism/internal/pubsub/frontends/aws_sqs_receive"
	azuresbtopicsfront "github.com/e6qu/shimanism/internal/pubsub/frontends/azure_servicebus_topics"
	gcppubsubpsfront "github.com/e6qu/shimanism/internal/pubsub/frontends/gcp_pubsub"
	awsbackend "github.com/e6qu/shimanism/services/pubsub/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/pubsub/backends/azure"
	gcpbackend "github.com/e6qu/shimanism/services/pubsub/backends/gcp"
	"github.com/e6qu/shimanism/services/pubsub/backends/inmem"
	natsbackend "github.com/e6qu/shimanism/services/pubsub/backends/nats"
)

func runPubsub(args []string) error {
	fs := flag.NewFlagSet("pubsub", flag.ContinueOnError)
	addr := fs.String("addr", ":9300", "address to listen on (SNS endpoint for the aws_sns frontend)")
	sqsAddr := fs.String("sqs-addr", ":9301", "secondary address for the SQS-shaped receive surface (aws_sns frontend only)")
	frontendName := fs.String("frontend", "aws_sns",
		"frontend wire protocol: aws_sns, gcp_pubsub, azure_servicebus_topics")
	backendName := fs.String("backend", "inmem",
		"backend: inmem, nats, aws, gcp, azure")

	natsURL := fs.String("nats-url", envOr("NATS_URL", ""), "NATS JetStream URL")
	gcpProject := fs.String("gcp-project", envOr("GCP_PROJECT_ID", ""), "GCP project ID")
	azureConn := fs.String("azure-connection-string",
		envOr("AZURE_SERVICEBUS_CONNECTION_STRING", ""),
		"Azure Service Bus connection string")
	awsRegion := fs.String("aws-region", envOr("AWS_REGION", "us-east-1"), "AWS region for the SNS+SQS backend")
	awsAccount := fs.String("aws-account", envOr("AWS_ACCOUNT_ID", "000000000000"), "AWS account ID used in ARNs")
	awsSnsEndpoint := fs.String("aws-sns-endpoint", envOr("AWS_SNS_ENDPOINT", ""), "AWS SNS endpoint override")
	awsSqsEndpoint := fs.String("aws-sqs-endpoint", envOr("AWS_SQS_ENDPOINT", ""), "AWS SQS endpoint override")

	if err := fs.Parse(args); err != nil {
		return err
	}

	backend, err := buildPubsubBackend(*backendName, pubsubBackendConfig{
		natsURL:        *natsURL,
		gcpProject:     *gcpProject,
		azureConn:      *azureConn,
		awsRegion:      *awsRegion,
		awsAccount:     *awsAccount,
		awsSnsEndpoint: *awsSnsEndpoint,
		awsSqsEndpoint: *awsSqsEndpoint,
	})
	if err != nil {
		return err
	}

	switch *frontendName {
	case "aws_sns":
		sns := awssnsfront.New(backend)
		sqs := awssqsreceivefront.New(backend)
		errCh := make(chan error, 2)
		go func() {
			fmt.Fprintf(os.Stderr, "shim pubsub: frontend=aws_sns sns-addr=%s\n", *addr)
			errCh <- http.ListenAndServe(*addr, sns)
		}()
		go func() {
			fmt.Fprintf(os.Stderr, "shim pubsub: frontend=aws_sns sqs-addr=%s\n", *sqsAddr)
			errCh <- http.ListenAndServe(*sqsAddr, sqs)
		}()
		return <-errCh
	case "gcp_pubsub":
		fmt.Fprintf(os.Stderr, "shim pubsub: frontend=gcp_pubsub addr=%s\n", *addr)
		return http.ListenAndServe(*addr, gcppubsubpsfront.New(backend))
	case "azure_servicebus_topics":
		fmt.Fprintf(os.Stderr, "shim pubsub: frontend=azure_servicebus_topics addr=%s\n", *addr)
		return http.ListenAndServe(*addr, azuresbtopicsfront.New(backend))
	default:
		return fmt.Errorf("unknown frontend %q (valid: aws_sns, gcp_pubsub, azure_servicebus_topics)", *frontendName)
	}
}

type pubsubBackendConfig struct {
	natsURL        string
	gcpProject     string
	azureConn      string
	awsRegion      string
	awsAccount     string
	awsSnsEndpoint string
	awsSqsEndpoint string
}

func buildPubsubBackend(name string, cfg pubsubBackendConfig) (domain.Pubsub, error) {
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
		return natsbackend.New(nc)
	case "aws":
		awsConf, err := awscfg.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		snsOpts := []func(*awssns.Options){}
		if cfg.awsSnsEndpoint != "" {
			snsOpts = append(snsOpts, func(o *awssns.Options) {
				o.BaseEndpoint = awsapi.String(cfg.awsSnsEndpoint)
			})
		}
		sqsOpts := []func(*awssqs.Options){}
		if cfg.awsSqsEndpoint != "" {
			sqsOpts = append(sqsOpts, func(o *awssqs.Options) {
				o.BaseEndpoint = awsapi.String(cfg.awsSqsEndpoint)
			})
		}
		return awsbackend.New(
			awssns.NewFromConfig(awsConf, snsOpts...),
			awssqs.NewFromConfig(awsConf, sqsOpts...),
			awsbackend.Config{Region: cfg.awsRegion, Account: cfg.awsAccount},
		), nil
	case "gcp":
		if cfg.gcpProject == "" {
			return nil, fmt.Errorf("gcp backend requires -gcp-project (or GCP_PROJECT_ID)")
		}
		svc, err := pubsubraw.NewService(context.Background(), option.WithUserAgent("shimanism-pubsub"))
		if err != nil {
			return nil, fmt.Errorf("connect to GCP Pub/Sub: %w", err)
		}
		return gcpbackend.New(svc, gcpbackend.Config{ProjectID: cfg.gcpProject}), nil
	case "azure":
		if cfg.azureConn == "" {
			return nil, fmt.Errorf("azure backend requires -azure-connection-string (or AZURE_SERVICEBUS_CONNECTION_STRING)")
		}
		return azurebackend.New(azurebackend.Config{ConnectionString: cfg.azureConn})
	default:
		return nil, fmt.Errorf("unknown backend %q (valid: inmem, nats, aws, gcp, azure)", name)
	}
}
