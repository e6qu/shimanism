// Package cnpg is the CloudNativePG backend for shimanism's rdbms
// service, acting as the K8s peer per PLAN.md Phase 5.
//
// Topology:
//
//   - Domain instance → `postgresql.cnpg.io/v1` `Cluster` CR.
//     CloudNativePG's operator creates an underlying StatefulSet,
//     a `<name>-rw` Service for the writer endpoint, and a
//     `<name>-app` Secret with master credentials.
//   - Domain snapshot → `postgresql.cnpg.io/v1` `Backup` CR.
//   - DescribeInstance reads Cluster status, the `-rw` Service for
//     the host, and the `-app` Secret for credentials. No shim-side
//     cache; every Describe re-reads.
//
// The shim uses a dynamic client + unstructured CRs rather than
// importing the cnpg Go API module — keeps the dependency footprint
// small and means cnpg version bumps don't force a code change.
//
// **Postgres only this phase.** MySQL support via MySQL-Operator is
// a follow-on; CreateInstance with EngineMySQL returns
// UnsupportedEngine.
package cnpg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
)

var (
	clusterGVR = schema.GroupVersionResource{
		Group:    "postgresql.cnpg.io",
		Version:  "v1",
		Resource: "clusters",
	}
	backupGVR = schema.GroupVersionResource{
		Group:    "postgresql.cnpg.io",
		Version:  "v1",
		Resource: "backups",
	}
)

type Config struct {
	// Namespace is where the shim creates Cluster + Backup CRs.
	Namespace string
	// PostgresImage optionally pins the cnpg image; empty lets the
	// operator pick its default.
	PostgresImage string
}

type Backend struct {
	dyn       dynamic.Interface
	core      kubernetes.Interface
	namespace string
	image     string
}

func New(dyn dynamic.Interface, core kubernetes.Interface, cfg Config) *Backend {
	ns := cfg.Namespace
	if ns == "" {
		ns = "default"
	}
	return &Backend{dyn: dyn, core: core, namespace: ns, image: cfg.PostgresImage}
}

var _ domain.RDBMS = (*Backend)(nil)

// ----------------------------------------------------------------------
// Instances
// ----------------------------------------------------------------------

func (b *Backend) CreateInstance(ctx context.Context, name string, opt domain.CreateInstanceOptions) (domain.CreateInstanceResult, error) {
	if opt.Engine != domain.EnginePostgres {
		return domain.CreateInstanceResult{}, domain.UnsupportedEngine(
			"CloudNativePG backend only supports the Postgres engine in this phase")
	}
	if opt.MasterUsername == "" {
		return domain.CreateInstanceResult{}, domain.InvalidArgument("MasterUsername is required")
	}
	password := opt.MasterPassword
	revealed := ""
	if password == "" {
		password = newPassword()
		revealed = password
	}

	// 1. Create the credentials Secret cnpg references via initdb.
	secretName := name + "-shim-creds"
	if _, err := b.core.CoreV1().Secrets(b.namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: b.namespace,
			Labels:    map[string]string{"shim.rdbms/instance": name},
		},
		Type: corev1.SecretTypeBasicAuth,
		StringData: map[string]string{
			"username": opt.MasterUsername,
			"password": password,
		},
	}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return domain.CreateInstanceResult{}, fmt.Errorf("create master-creds Secret: %w", err)
	}

	// 2. Create the Cluster CR.
	storage := opt.AllocatedStorageGB
	if storage <= 0 {
		storage = 1
	}
	cluster := &unstructured.Unstructured{}
	cluster.SetAPIVersion("postgresql.cnpg.io/v1")
	cluster.SetKind("Cluster")
	cluster.SetName(name)
	cluster.SetNamespace(b.namespace)
	cluster.Object["spec"] = map[string]interface{}{
		"instances": int64(1),
		"bootstrap": map[string]interface{}{
			"initdb": map[string]interface{}{
				"database": "postgres",
				"owner":    opt.MasterUsername,
				"secret":   map[string]interface{}{"name": secretName},
			},
		},
		"storage": map[string]interface{}{
			"size": resource.NewQuantity(int64(storage)*1024*1024*1024, resource.BinarySI).String(),
		},
	}
	if b.image != "" {
		cluster.Object["spec"].(map[string]interface{})["imageName"] = b.image
	}
	if _, err := b.dyn.Resource(clusterGVR).Namespace(b.namespace).Create(ctx, cluster, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.CreateInstanceResult{}, domain.InstanceAlreadyExists(name)
		}
		return domain.CreateInstanceResult{}, fmt.Errorf("create Cluster CR: %w", err)
	}

	return domain.CreateInstanceResult{
		Instance: domain.Instance{
			Name:               name,
			Engine:             domain.EnginePostgres,
			EngineVersion:      opt.EngineVersion,
			Status:             domain.StatusCreating,
			AllocatedStorageGB: storage,
			InstanceClass:      opt.InstanceClass,
			CreatedAt:          time.Now().UTC(),
		},
		MasterPassword: revealed,
	}, nil
}

func (b *Backend) DeleteInstance(ctx context.Context, name string) error {
	if err := b.dyn.Resource(clusterGVR).Namespace(b.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return domain.NoSuchInstance(name)
		}
		return fmt.Errorf("delete Cluster CR: %w", err)
	}
	// Best-effort cleanup of the credentials Secret. The cnpg
	// operator may also create operator-owned Secrets that need to
	// outlive the Cluster's deletion; the shim only deletes the
	// one it explicitly created.
	_ = b.core.CoreV1().Secrets(b.namespace).Delete(ctx, name+"-shim-creds", metav1.DeleteOptions{})
	return nil
}

func (b *Backend) DescribeInstance(ctx context.Context, name string) (domain.Instance, error) {
	cluster, err := b.dyn.Resource(clusterGVR).Namespace(b.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.Instance{}, domain.NoSuchInstance(name)
		}
		return domain.Instance{}, fmt.Errorf("get Cluster CR: %w", err)
	}
	inst := b.clusterToInstance(ctx, cluster)
	return inst, nil
}

func (b *Backend) ListInstances(ctx context.Context, opt domain.ListInstancesOptions) (domain.ListInstancesResult, error) {
	list, err := b.dyn.Resource(clusterGVR).Namespace(b.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return domain.ListInstancesResult{}, fmt.Errorf("list Cluster CRs: %w", err)
	}
	res := domain.ListInstancesResult{}
	for i := range list.Items {
		c := &list.Items[i]
		if opt.Prefix != "" && !strings.HasPrefix(c.GetName(), opt.Prefix) {
			continue
		}
		res.Instances = append(res.Instances, b.clusterToInstance(ctx, c))
		if opt.MaxResults > 0 && len(res.Instances) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) ModifyInstance(ctx context.Context, name string, opt domain.ModifyInstanceOptions) error {
	cluster, err := b.dyn.Resource(clusterGVR).Namespace(b.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.NoSuchInstance(name)
		}
		return err
	}
	spec, _ := cluster.Object["spec"].(map[string]interface{})
	if spec == nil {
		spec = map[string]interface{}{}
		cluster.Object["spec"] = spec
	}
	if opt.AllocatedStorageGB > 0 {
		storage, _ := spec["storage"].(map[string]interface{})
		if storage == nil {
			storage = map[string]interface{}{}
		}
		storage["size"] = resource.NewQuantity(int64(opt.AllocatedStorageGB)*1024*1024*1024, resource.BinarySI).String()
		spec["storage"] = storage
	}
	if _, err := b.dyn.Resource(clusterGVR).Namespace(b.namespace).Update(ctx, cluster, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update Cluster CR: %w", err)
	}
	if opt.MasterPassword != "" {
		// Patch the credentials Secret. cnpg picks up the change on
		// the next reconcile.
		secret, err := b.core.CoreV1().Secrets(b.namespace).Get(ctx, name+"-shim-creds", metav1.GetOptions{})
		if err == nil {
			if secret.StringData == nil {
				secret.StringData = map[string]string{}
			}
			secret.StringData["password"] = opt.MasterPassword
			_, _ = b.core.CoreV1().Secrets(b.namespace).Update(ctx, secret, metav1.UpdateOptions{})
		}
	}
	return nil
}

func (b *Backend) RebootInstance(ctx context.Context, name string) error {
	// Cnpg rolls the cluster when the annotation `cnpg.io/reloadedAt`
	// changes. Patch it with the current time.
	cluster, err := b.dyn.Resource(clusterGVR).Namespace(b.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.NoSuchInstance(name)
		}
		return err
	}
	anns := cluster.GetAnnotations()
	if anns == nil {
		anns = map[string]string{}
	}
	anns["cnpg.io/reloadedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	cluster.SetAnnotations(anns)
	if _, err := b.dyn.Resource(clusterGVR).Namespace(b.namespace).Update(ctx, cluster, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("annotate Cluster CR for restart: %w", err)
	}
	return nil
}

// ----------------------------------------------------------------------
// Snapshots
// ----------------------------------------------------------------------

func (b *Backend) CreateSnapshot(ctx context.Context, instance, snapshotID string) (domain.Snapshot, error) {
	// CloudNativePG expects an external object store configured on
	// the Cluster (.spec.backup.barmanObjectStore). Without it,
	// Backup CRs fail. The shim still creates the Backup CR — the
	// failure will surface via DescribeSnapshot Status; the shim
	// reports the operator's truth rather than lying about it.
	backup := &unstructured.Unstructured{}
	backup.SetAPIVersion("postgresql.cnpg.io/v1")
	backup.SetKind("Backup")
	backup.SetName(snapshotID)
	backup.SetNamespace(b.namespace)
	backup.Object["spec"] = map[string]interface{}{
		"cluster": map[string]interface{}{"name": instance},
	}
	if _, err := b.dyn.Resource(backupGVR).Namespace(b.namespace).Create(ctx, backup, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.Snapshot{}, domain.SnapshotAlreadyExists(snapshotID)
		}
		return domain.Snapshot{}, fmt.Errorf("create Backup CR: %w", err)
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
	if err := b.dyn.Resource(backupGVR).Namespace(b.namespace).Delete(ctx, snapshotID, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return domain.NoSuchSnapshot(snapshotID)
		}
		return fmt.Errorf("delete Backup CR: %w", err)
	}
	return nil
}

func (b *Backend) DescribeSnapshot(ctx context.Context, snapshotID string) (domain.Snapshot, error) {
	backup, err := b.dyn.Resource(backupGVR).Namespace(b.namespace).Get(ctx, snapshotID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.Snapshot{}, domain.NoSuchSnapshot(snapshotID)
		}
		return domain.Snapshot{}, err
	}
	return b.backupToSnapshot(backup), nil
}

func (b *Backend) ListSnapshots(ctx context.Context, opt domain.ListSnapshotsOptions) (domain.ListSnapshotsResult, error) {
	list, err := b.dyn.Resource(backupGVR).Namespace(b.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return domain.ListSnapshotsResult{}, err
	}
	res := domain.ListSnapshotsResult{}
	for i := range list.Items {
		s := b.backupToSnapshot(&list.Items[i])
		if opt.Instance != "" && s.Instance != opt.Instance {
			continue
		}
		res.Snapshots = append(res.Snapshots, s)
		if opt.MaxResults > 0 && len(res.Snapshots) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) RestoreFromSnapshot(ctx context.Context, snapshotID, newInstanceName string) (domain.CreateInstanceResult, error) {
	// Restore via cnpg's bootstrap.recovery pointing at the source
	// Backup. The new cluster reuses the source's credentials Secret
	// (cnpg replays it during recovery).
	_, err := b.DescribeSnapshot(ctx, snapshotID)
	if err != nil {
		return domain.CreateInstanceResult{}, err
	}
	cluster := &unstructured.Unstructured{}
	cluster.SetAPIVersion("postgresql.cnpg.io/v1")
	cluster.SetKind("Cluster")
	cluster.SetName(newInstanceName)
	cluster.SetNamespace(b.namespace)
	cluster.Object["spec"] = map[string]interface{}{
		"instances": int64(1),
		"bootstrap": map[string]interface{}{
			"recovery": map[string]interface{}{
				"backup": map[string]interface{}{"name": snapshotID},
			},
		},
		"storage": map[string]interface{}{
			"size": "1Gi",
		},
	}
	if _, err := b.dyn.Resource(clusterGVR).Namespace(b.namespace).Create(ctx, cluster, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.CreateInstanceResult{}, domain.InstanceAlreadyExists(newInstanceName)
		}
		return domain.CreateInstanceResult{}, fmt.Errorf("create restore Cluster CR: %w", err)
	}
	return domain.CreateInstanceResult{
		Instance: domain.Instance{
			Name:      newInstanceName,
			Engine:    domain.EnginePostgres,
			Status:    domain.StatusCreating,
			CreatedAt: time.Now().UTC(),
		},
	}, nil
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func (b *Backend) clusterToInstance(ctx context.Context, cluster *unstructured.Unstructured) domain.Instance {
	name := cluster.GetName()
	inst := domain.Instance{
		Name:      name,
		Engine:    domain.EnginePostgres,
		Status:    statusFromCluster(cluster),
		CreatedAt: cluster.GetCreationTimestamp().Time,
	}
	// Storage size from spec.
	if spec, ok := cluster.Object["spec"].(map[string]interface{}); ok {
		if storage, ok := spec["storage"].(map[string]interface{}); ok {
			if size, ok := storage["size"].(string); ok {
				if q, err := resource.ParseQuantity(size); err == nil {
					inst.AllocatedStorageGB = int(q.Value() / (1024 * 1024 * 1024))
				}
			}
		}
	}
	// Resolve Connection only when ready — the -rw Service + the
	// creds Secret both exist after the operator finishes bootstrap.
	if inst.Status == domain.StatusAvailable {
		svc, err := b.core.CoreV1().Services(b.namespace).Get(ctx, name+"-rw", metav1.GetOptions{})
		if err == nil {
			inst.Connection = domain.Connection{
				Host:         svc.Name + "." + svc.Namespace + ".svc.cluster.local",
				Port:         5432,
				DatabaseName: "postgres",
			}
			// Username from the credentials Secret.
			if secret, err := b.core.CoreV1().Secrets(b.namespace).Get(ctx, name+"-shim-creds", metav1.GetOptions{}); err == nil {
				inst.Connection.MasterUsername = string(secret.Data["username"])
			}
		}
	}
	return inst
}

func (b *Backend) backupToSnapshot(u *unstructured.Unstructured) domain.Snapshot {
	out := domain.Snapshot{
		ID:        u.GetName(),
		Engine:    domain.EnginePostgres,
		Status:    statusFromBackup(u),
		CreatedAt: u.GetCreationTimestamp().Time,
	}
	if spec, ok := u.Object["spec"].(map[string]interface{}); ok {
		if cluster, ok := spec["cluster"].(map[string]interface{}); ok {
			if n, ok := cluster["name"].(string); ok {
				out.Instance = n
			}
		}
	}
	return out
}

// statusFromCluster reads .status.conditions and returns the
// closest domain.Status.
func statusFromCluster(u *unstructured.Unstructured) domain.Status {
	if u.GetDeletionTimestamp() != nil {
		return domain.StatusDeleting
	}
	status, ok := u.Object["status"].(map[string]interface{})
	if !ok {
		return domain.StatusCreating
	}
	if ready, ok := status["readyInstances"]; ok {
		if r, ok := ready.(int64); ok && r > 0 {
			return domain.StatusAvailable
		}
		if r, ok := ready.(float64); ok && r > 0 {
			return domain.StatusAvailable
		}
	}
	return domain.StatusCreating
}

func statusFromBackup(u *unstructured.Unstructured) domain.Status {
	status, ok := u.Object["status"].(map[string]interface{})
	if !ok {
		return domain.StatusCreating
	}
	if phase, ok := status["phase"].(string); ok {
		switch phase {
		case "completed":
			return domain.StatusAvailable
		case "failed":
			return domain.StatusUnknown
		default:
			return domain.StatusCreating
		}
	}
	return domain.StatusCreating
}

func newPassword() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
