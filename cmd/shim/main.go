// Command shim is the entry point for the shimanism protocol-translation
// proxy. It runs the shim as a network service: a chosen wire-protocol
// frontend sits in front of a chosen backend, so any cloud's official
// SDK / CLI / Terraform provider can drive it via the standard
// endpoint-override path.
//
// Subcommands:
//
//	shim version           — print version and exit.
//	shim storage [flags]   — run the storage service.
//	shim secrets [flags]   — run the secrets service.
//	shim queue   [flags]   — run the queue service.
//	shim pubsub  [flags]   — run the pubsub (topic fanout) service.
//	shim rdbms   [flags]   — run the rdbms (managed-DB control plane) service.
//	shim cache   [flags]   — run the cache (managed-Redis control plane) service.
//	shim functions [flags] — run the functions (container-image deploy) service.
//
// Each service subcommand selects a backend via -backend=<name> and a
// frontend via -frontend=<name>. Storage backends: inmem, minio, aws,
// gcs, azureblob. Storage frontends: aws_s3, gcs, azure_blob. Secrets
// backends: inmem, vault, aws, gcp, azure. Secrets frontends:
// aws_secretsmanager, gcp_secretmanager, azure_keyvault. Queue
// backends: inmem, nats, aws, gcp, azure. Queue frontends: aws_sqs,
// gcp_pubsub, azure_servicebus. The K8s peer (deploy/k8s/peer/) uses
// frontend=aws_s3 + backend=minio for storage,
// frontend=aws_secretsmanager + backend=vault for secrets, and
// frontend=aws_sqs + backend=nats for queue.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	gcsstorage "cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/e6qu/shimanism/internal/restxml"
	"github.com/e6qu/shimanism/internal/storage/domain"
	awsfront "github.com/e6qu/shimanism/internal/storage/frontends/aws_s3"
	azurefront "github.com/e6qu/shimanism/internal/storage/frontends/azure_blob"
	gcsfront "github.com/e6qu/shimanism/internal/storage/frontends/gcs"
	awsbackend "github.com/e6qu/shimanism/services/storage/backends/aws"
	azureblobbackend "github.com/e6qu/shimanism/services/storage/backends/azureblob"
	gcsbackend "github.com/e6qu/shimanism/services/storage/backends/gcs"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
	miniobackend "github.com/e6qu/shimanism/services/storage/backends/minio"
	storagegen "github.com/e6qu/shimanism/services/storage/gen"
)

const version = "0.9.0-phase-8"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println(version)
	case "storage":
		if err := runStorage(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "shim storage:", err)
			os.Exit(1)
		}
	case "secrets":
		if err := runSecrets(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "shim secrets:", err)
			os.Exit(1)
		}
	case "queue":
		if err := runQueue(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "shim queue:", err)
			os.Exit(1)
		}
	case "pubsub":
		if err := runPubsub(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "shim pubsub:", err)
			os.Exit(1)
		}
	case "rdbms":
		if err := runRDBMS(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "shim rdbms:", err)
			os.Exit(1)
		}
	case "cache":
		if err := runCache(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "shim cache:", err)
			os.Exit(1)
		}
	case "functions":
		if err := runFunctions(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "shim functions:", err)
			os.Exit(1)
		}
	case "apigateway":
		if err := runAPIGateway(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "shim apigateway:", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintln(os.Stderr, "shim: unknown subcommand:", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: shim <subcommand> [flags]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  version            Print the shim version and exit.")
	fmt.Fprintln(os.Stderr, "  storage [flags]    Run the storage service.")
	fmt.Fprintln(os.Stderr, "  secrets [flags]    Run the secrets service.")
	fmt.Fprintln(os.Stderr, "  queue   [flags]    Run the queue service.")
	fmt.Fprintln(os.Stderr, "  pubsub  [flags]    Run the pubsub (topic fanout) service.")
	fmt.Fprintln(os.Stderr, "  rdbms   [flags]    Run the rdbms (managed-DB control plane) service.")
	fmt.Fprintln(os.Stderr, "  cache   [flags]    Run the cache (managed-Redis control plane) service.")
	fmt.Fprintln(os.Stderr, "  functions [flags]  Run the functions (container-image deploy) service.")
	fmt.Fprintln(os.Stderr, "  apigateway [flags] Run the apigateway (HTTP-route control plane) service.")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Run `shim <subcommand> -h` for per-service flags.")
}

func runStorage(args []string) error {
	fs := flag.NewFlagSet("storage", flag.ContinueOnError)
	addr := fs.String("addr", ":9000", "address to listen on")
	frontendName := fs.String("frontend", "aws_s3", "frontend wire protocol: aws_s3, gcs, azure_blob")
	backendName := fs.String("backend", "inmem", "backend: inmem, minio, aws, gcs, azureblob")
	minioEndpoint := fs.String("minio-endpoint", envOr("MINIO_ENDPOINT", ""), "MinIO endpoint host:port")
	minioAccess := fs.String("minio-access-key", envOr("MINIO_ACCESS_KEY", "minioadmin"), "MinIO access key")
	minioSecret := fs.String("minio-secret-key", envOr("MINIO_SECRET_KEY", "minioadmin"), "MinIO secret key")
	awsEndpoint := fs.String("aws-endpoint", envOr("AWS_S3_ENDPOINT", ""), "AWS S3 endpoint override (empty = default)")
	gcsProject := fs.String("gcs-project", envOr("GCS_PROJECT_ID", ""), "GCS project ID")
	azureConn := fs.String("azure-connection-string", envOr("AZURE_STORAGE_CONNECTION_STRING", ""), "Azure storage account connection string")
	azureRegion := fs.String("azure-region", envOr("AZURE_BLOB_REGION", "us-east-1"), "Azure storage account region")
	if err := fs.Parse(args); err != nil {
		return err
	}

	backend, err := buildBackend(*backendName, backendConfig{
		minioEndpoint: *minioEndpoint,
		minioAccess:   *minioAccess,
		minioSecret:   *minioSecret,
		awsEndpoint:   *awsEndpoint,
		gcsProject:    *gcsProject,
		azureConn:     *azureConn,
		azureRegion:   *azureRegion,
	})
	if err != nil {
		return err
	}

	handler, err := buildFrontend(*frontendName, backend)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "shim storage: frontend=%s backend=%s addr=%s\n", *frontendName, *backendName, *addr)
	return http.ListenAndServe(*addr, handler)
}

// buildFrontend wires the chosen wire-protocol frontend to a backend
// implementation. The frontend translates the cloud's published API
// into calls on the neutral domain.Storage interface; the backend
// translates that interface into the destination cloud's native API.
func buildFrontend(name string, backend domain.Storage) (http.Handler, error) {
	switch name {
	case "aws_s3":
		adapter := awsfront.New(backend)
		router := &restxml.Router{}
		storagegen.RegisterAmazonS3Routes(router, adapter)
		return router, nil
	case "gcs":
		return gcsfront.New(backend), nil
	case "azure_blob":
		return azurefront.New(backend), nil
	default:
		return nil, fmt.Errorf("unknown frontend %q (valid: aws_s3, gcs, azure_blob)", name)
	}
}

type backendConfig struct {
	minioEndpoint string
	minioAccess   string
	minioSecret   string
	awsEndpoint   string
	gcsProject    string
	azureConn     string
	azureRegion   string
}

func buildBackend(name string, cfg backendConfig) (domain.Storage, error) {
	switch name {
	case "inmem":
		return inmem.New(), nil
	case "minio":
		if cfg.minioEndpoint == "" {
			return nil, fmt.Errorf("minio backend requires -minio-endpoint (or MINIO_ENDPOINT)")
		}
		return miniobackend.New(miniobackend.Config{
			Endpoint:  cfg.minioEndpoint,
			AccessKey: cfg.minioAccess,
			SecretKey: cfg.minioSecret,
		})
	case "aws":
		awsCfg, err := config.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		opts := []func(*awss3.Options){}
		if cfg.awsEndpoint != "" {
			opts = append(opts, func(o *awss3.Options) {
				o.BaseEndpoint = aws.String(cfg.awsEndpoint)
				o.UsePathStyle = true
			})
		}
		return awsbackend.New(awss3.NewFromConfig(awsCfg, opts...)), nil
	case "gcs":
		if cfg.gcsProject == "" {
			return nil, fmt.Errorf("gcs backend requires -gcs-project (or GCS_PROJECT_ID)")
		}
		client, err := gcsstorage.NewClient(context.Background())
		if err != nil {
			return nil, fmt.Errorf("connect to GCS: %w", err)
		}
		return gcsbackend.New(client, gcsbackend.Config{ProjectID: cfg.gcsProject}), nil
	case "azureblob":
		if cfg.azureConn == "" {
			return nil, fmt.Errorf("azureblob backend requires -azure-connection-string (or AZURE_STORAGE_CONNECTION_STRING)")
		}
		client, err := azblob.NewClientFromConnectionString(cfg.azureConn, nil)
		if err != nil {
			return nil, fmt.Errorf("connect to Azure Blob: %w", err)
		}
		return azureblobbackend.New(client, cfg.azureRegion), nil
	default:
		return nil, fmt.Errorf("unknown backend %q (valid: inmem, minio, aws, gcs, azureblob)", name)
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
