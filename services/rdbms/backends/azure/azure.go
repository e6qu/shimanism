// Package azure is the Azure DB Admin backend for shimanism's
// rdbms service. Postgres-only in this phase via
// `armpostgresqlflexibleservers/v4`; MySQL via
// `armmysqlflexibleservers` is a follow-on if needed.
//
// Azure flexible-servers admin is ARM-based: every mutating call
// returns a long-running operation. The backend deliberately
// doesn't `Wait()` — clients poll DescribeInstance, same async
// model as the AWS / GCP / cnpg backends.
package azure

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	armpg "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers/v4"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
)

type Config struct {
	SubscriptionID string
	ResourceGroup  string
	Location       string // default eastus
	Credential     azcore.TokenCredential
	// ClientOptions, if non-nil, is forwarded to the armpg factory.
	// Used by the sockerless test lane to point the ARM endpoint at a
	// local simulator and inject a self-signed-cert-tolerant transport.
	ClientOptions *arm.ClientOptions
}

type Backend struct {
	servers       *armpg.ServersClient
	backups       *armpg.BackupsClient
	subscription  string
	resourceGroup string
	location      string
}

func New(cfg Config) (*Backend, error) {
	if cfg.SubscriptionID == "" || cfg.ResourceGroup == "" {
		return nil, fmt.Errorf("azure rdbms: SubscriptionID and ResourceGroup are required")
	}
	if cfg.Credential == nil {
		return nil, fmt.Errorf("azure rdbms: TokenCredential is required")
	}
	loc := cfg.Location
	if loc == "" {
		loc = "eastus"
	}
	factory, err := armpg.NewClientFactory(cfg.SubscriptionID, cfg.Credential, cfg.ClientOptions)
	if err != nil {
		return nil, fmt.Errorf("azure rdbms client factory: %w", err)
	}
	return &Backend{
		servers:       factory.NewServersClient(),
		backups:       factory.NewBackupsClient(),
		subscription:  cfg.SubscriptionID,
		resourceGroup: cfg.ResourceGroup,
		location:      loc,
	}, nil
}

var _ domain.RDBMS = (*Backend)(nil)

func domainStatusFromAzure(s string) domain.Status {
	switch strings.ToLower(s) {
	case "ready", "succeeded":
		return domain.StatusAvailable
	case "provisioning", "creating", "starting":
		return domain.StatusCreating
	case "updating":
		return domain.StatusModifying
	case "restarting":
		return domain.StatusRebooting
	case "dropping", "deleting", "disabled":
		return domain.StatusDeleting
	default:
		return domain.StatusCreating
	}
}

func (b *Backend) instanceToDomain(in *armpg.Server) domain.Instance {
	out := domain.Instance{
		Name:   *in.Name,
		Engine: domain.EnginePostgres,
	}
	if in.Properties != nil {
		if in.Properties.State != nil {
			out.Status = domainStatusFromAzure(string(*in.Properties.State))
		}
		if in.Properties.Version != nil {
			out.EngineVersion = string(*in.Properties.Version)
		}
		if in.Properties.Storage != nil && in.Properties.Storage.StorageSizeGB != nil {
			out.AllocatedStorageGB = int(*in.Properties.Storage.StorageSizeGB)
		}
	}
	if in.SKU != nil && in.SKU.Name != nil {
		out.InstanceClass = *in.SKU.Name
	}
	if out.Status == domain.StatusAvailable && in.Properties != nil && in.Properties.FullyQualifiedDomainName != nil {
		out.Connection = domain.Connection{
			Host:           *in.Properties.FullyQualifiedDomainName,
			Port:           5432,
			MasterUsername: "shimadmin",
			DatabaseName:   "postgres",
			EngineVersion:  out.EngineVersion,
		}
		if in.Properties.AdministratorLogin != nil {
			out.Connection.MasterUsername = *in.Properties.AdministratorLogin
		}
	}
	return out
}

// ----------------------------------------------------------------------
// Instances
// ----------------------------------------------------------------------

func (b *Backend) CreateInstance(ctx context.Context, name string, opt domain.CreateInstanceOptions) (domain.CreateInstanceResult, error) {
	if opt.Engine != domain.EnginePostgres {
		return domain.CreateInstanceResult{}, domain.UnsupportedEngine(
			"Azure rdbms backend supports only the Postgres engine in this phase")
	}
	pw := opt.MasterPassword
	revealed := ""
	if pw == "" {
		pw = newPassword()
		revealed = pw
	}
	tier := opt.InstanceClass
	if tier == "" {
		tier = "Standard_B1ms"
	}
	storage := int32(opt.AllocatedStorageGB)
	if storage == 0 {
		storage = 32
	}
	version := opt.EngineVersion
	if version == "" {
		version = "15"
	}
	srv := armpg.Server{
		Location: &b.location,
		SKU: &armpg.SKU{
			Name: &tier,
			Tier: skuTier(tier),
		},
		Properties: &armpg.ServerProperties{
			AdministratorLogin:         &opt.MasterUsername,
			AdministratorLoginPassword: &pw,
			Storage: &armpg.Storage{
				StorageSizeGB: &storage,
			},
			Version: serverVersion(version),
		},
	}
	if _, err := b.servers.BeginCreate(ctx, b.resourceGroup, name, srv, nil); err != nil {
		return domain.CreateInstanceResult{}, translateErr(err, "instance", name)
	}
	return domain.CreateInstanceResult{
		Instance: domain.Instance{
			Name:               name,
			Engine:             domain.EnginePostgres,
			EngineVersion:      version,
			Status:             domain.StatusCreating,
			AllocatedStorageGB: int(storage),
			InstanceClass:      tier,
			CreatedAt:          time.Now().UTC(),
		},
		MasterPassword: revealed,
	}, nil
}

func (b *Backend) DeleteInstance(ctx context.Context, name string) error {
	if _, err := b.servers.BeginDelete(ctx, b.resourceGroup, name, nil); err != nil {
		return translateErr(err, "instance", name)
	}
	return nil
}

func (b *Backend) DescribeInstance(ctx context.Context, name string) (domain.Instance, error) {
	out, err := b.servers.Get(ctx, b.resourceGroup, name, nil)
	if err != nil {
		return domain.Instance{}, translateErr(err, "instance", name)
	}
	return b.instanceToDomain(&out.Server), nil
}

func (b *Backend) ListInstances(ctx context.Context, opt domain.ListInstancesOptions) (domain.ListInstancesResult, error) {
	pager := b.servers.NewListByResourceGroupPager(b.resourceGroup, nil)
	res := domain.ListInstancesResult{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return res, translateErr(err, "instance", "")
		}
		for _, in := range page.Value {
			if in.Name == nil {
				continue
			}
			if opt.Prefix != "" && !strings.HasPrefix(*in.Name, opt.Prefix) {
				continue
			}
			res.Instances = append(res.Instances, b.instanceToDomain(in))
			if opt.MaxResults > 0 && len(res.Instances) >= opt.MaxResults {
				return res, nil
			}
		}
	}
	return res, nil
}

func (b *Backend) ModifyInstance(ctx context.Context, name string, opt domain.ModifyInstanceOptions) error {
	props := &armpg.ServerPropertiesForUpdate{}
	patch := armpg.ServerForUpdate{Properties: props}
	if opt.AllocatedStorageGB > 0 {
		sz := int32(opt.AllocatedStorageGB)
		props.Storage = &armpg.Storage{StorageSizeGB: &sz}
	}
	if opt.InstanceClass != "" {
		ic := opt.InstanceClass
		patch.SKU = &armpg.SKU{Name: &ic, Tier: skuTier(ic)}
	}
	if opt.MasterPassword != "" {
		props.AdministratorLoginPassword = &opt.MasterPassword
	}
	if _, err := b.servers.BeginUpdate(ctx, b.resourceGroup, name, patch, nil); err != nil {
		return translateErr(err, "instance", name)
	}
	return nil
}

func (b *Backend) RebootInstance(ctx context.Context, name string) error {
	if _, err := b.servers.BeginRestart(ctx, b.resourceGroup, name, nil); err != nil {
		return translateErr(err, "instance", name)
	}
	return nil
}

// ----------------------------------------------------------------------
// Snapshots (Backups)
// ----------------------------------------------------------------------

func (b *Backend) CreateSnapshot(ctx context.Context, instance, snapshotID string) (domain.Snapshot, error) {
	if _, err := b.backups.BeginCreate(ctx, b.resourceGroup, instance, snapshotID, nil); err != nil {
		return domain.Snapshot{}, translateErr(err, "snapshot", snapshotID)
	}
	return domain.Snapshot{
		ID:        snapshotID,
		Instance:  instance,
		Engine:    domain.EnginePostgres,
		Status:    domain.StatusCreating,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func (b *Backend) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	// Azure backup deletion would require the instance name; the
	// domain identifies snapshots by ID only. Returning
	// InvalidArgument signals out-of-intersection cleanly.
	return domain.InvalidArgument(
		"Azure rdbms DeleteSnapshot needs the instance + backup ID — out of intersection at this phase")
}

func (b *Backend) DescribeSnapshot(ctx context.Context, snapshotID string) (domain.Snapshot, error) {
	return domain.Snapshot{}, domain.NoSuchSnapshot(snapshotID)
}

func (b *Backend) ListSnapshots(ctx context.Context, opt domain.ListSnapshotsOptions) (domain.ListSnapshotsResult, error) {
	if opt.Instance == "" {
		return domain.ListSnapshotsResult{}, domain.InvalidArgument(
			"Azure rdbms ListSnapshots requires an instance filter")
	}
	pager := b.backups.NewListByServerPager(b.resourceGroup, opt.Instance, nil)
	res := domain.ListSnapshotsResult{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return res, translateErr(err, "snapshot", "")
		}
		for _, r := range page.Value {
			if r.Name == nil {
				continue
			}
			res.Snapshots = append(res.Snapshots, domain.Snapshot{
				ID:       *r.Name,
				Instance: opt.Instance,
				Engine:   domain.EnginePostgres,
				Status:   domain.StatusAvailable,
			})
			if opt.MaxResults > 0 && len(res.Snapshots) >= opt.MaxResults {
				return res, nil
			}
		}
	}
	return res, nil
}

func (b *Backend) RestoreFromSnapshot(ctx context.Context, snapshotID, newInstanceName string) (domain.CreateInstanceResult, error) {
	return domain.CreateInstanceResult{}, domain.InvalidArgument(
		"Azure rdbms RestoreFromSnapshot via point-in-time-restore is not wired in this phase")
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func skuTier(name string) *armpg.SKUTier {
	switch {
	case strings.HasPrefix(name, "Burstable"), strings.HasPrefix(name, "Standard_B"):
		t := armpg.SKUTierBurstable
		return &t
	case strings.HasPrefix(name, "Standard_D"):
		t := armpg.SKUTierGeneralPurpose
		return &t
	case strings.HasPrefix(name, "Standard_E"), strings.HasPrefix(name, "Standard_M"):
		t := armpg.SKUTierMemoryOptimized
		return &t
	default:
		t := armpg.SKUTierBurstable
		return &t
	}
}

func serverVersion(s string) *armpg.ServerVersion {
	switch s {
	case "11":
		v := armpg.ServerVersionEleven
		return &v
	case "12":
		v := armpg.ServerVersionTwelve
		return &v
	case "13":
		v := armpg.ServerVersionThirteen
		return &v
	case "14":
		v := armpg.ServerVersionFourteen
		return &v
	case "15":
		v := armpg.ServerVersionFifteen
		return &v
	case "16":
		v := armpg.ServerVersionSixteen
		return &v
	default:
		v := armpg.ServerVersionFifteen
		return &v
	}
}

func translateErr(err error, kind, name string) error {
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		switch re.StatusCode {
		case 404:
			if kind == "snapshot" {
				return domain.NoSuchSnapshot(name)
			}
			return domain.NoSuchInstance(name)
		case 409:
			if kind == "snapshot" {
				return domain.SnapshotAlreadyExists(name)
			}
			return domain.InstanceAlreadyExists(name)
		}
	}
	return fmt.Errorf("azure %s %q: %w", kind, name, err)
}

func newPassword() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// silence unused-import lint when Azure's runtime sub-package isn't
// directly referenced.
var _ = arm.ClientOptions{}
