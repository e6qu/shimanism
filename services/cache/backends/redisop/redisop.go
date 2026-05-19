// Package redisop is the Redis Operator backend for shimanism's
// cache service, acting as the K8s peer.
//
// Targets the OT-CONTAINER-KIT Redis Operator (the most-used
// community operator), CRD: `redis.redis.opstreelabs.in/v1beta2`
// resource `Redis` for single-node deployments.
//
// Same pattern as Phase 5's cnpg backend: dynamic client +
// unstructured CRs (no operator-api module pulled, so operator
// version bumps don't force a recompile). Auth token stored in a
// Kubernetes Secret; the shim re-reads on each DescribeInstance.
package redisop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/e6qu/shimanism/internal/cache/domain"
)

var redisGVR = schema.GroupVersionResource{
	Group:    "redis.redis.opstreelabs.in",
	Version:  "v1beta2",
	Resource: "redis",
}

type Config struct {
	Namespace string
	Image     string // optional override
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
	return &Backend{dyn: dyn, core: core, namespace: ns, image: cfg.Image}
}

var _ domain.Cache = (*Backend)(nil)

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (b *Backend) CreateInstance(ctx context.Context, name string, opt domain.CreateInstanceOptions) (domain.CreateInstanceResult, error) {
	token := opt.AuthToken
	revealed := ""
	if token == "" {
		token = newToken()
		revealed = token
	}
	// Create the auth Secret the Redis CR references.
	secretName := name + "-shim-auth"
	if _, err := b.core.CoreV1().Secrets(b.namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: b.namespace,
			Labels:    map[string]string{"shim.cache/instance": name},
		},
		StringData: map[string]string{"password": token},
	}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return domain.CreateInstanceResult{}, fmt.Errorf("create auth Secret: %w", err)
	}

	redis := &unstructured.Unstructured{}
	redis.SetAPIVersion("redis.redis.opstreelabs.in/v1beta2")
	redis.SetKind("Redis")
	redis.SetName(name)
	redis.SetNamespace(b.namespace)
	image := b.image
	if image == "" {
		// OT-CONTAINER-KIT's published default; pinned for
		// reproducibility. The operator requires spec.kubernetesConfig.image
		// to be non-empty.
		image = "quay.io/opstree/redis:v7.0.12"
	}
	spec := map[string]interface{}{
		"kubernetesConfig": map[string]interface{}{
			"image":           image,
			"imagePullPolicy": "IfNotPresent",
			"redisSecret": map[string]interface{}{
				"name": secretName,
				"key":  "password",
			},
		},
	}
	redis.Object["spec"] = spec
	if _, err := b.dyn.Resource(redisGVR).Namespace(b.namespace).Create(ctx, redis, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.CreateInstanceResult{}, domain.InstanceAlreadyExists(name)
		}
		return domain.CreateInstanceResult{}, fmt.Errorf("create Redis CR: %w", err)
	}

	return domain.CreateInstanceResult{
		Instance: domain.Instance{
			Name:          name,
			EngineVersion: opt.EngineVersion,
			NodeType:      opt.NodeType,
			Status:        domain.StatusCreating,
			CreatedAt:     time.Now().UTC(),
		},
		AuthToken: revealed,
	}, nil
}

func (b *Backend) DeleteInstance(ctx context.Context, name string) error {
	if err := b.dyn.Resource(redisGVR).Namespace(b.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return domain.NoSuchInstance(name)
		}
		return fmt.Errorf("delete Redis CR: %w", err)
	}
	_ = b.core.CoreV1().Secrets(b.namespace).Delete(ctx, name+"-shim-auth", metav1.DeleteOptions{})
	return nil
}

func (b *Backend) DescribeInstance(ctx context.Context, name string) (domain.Instance, error) {
	redis, err := b.dyn.Resource(redisGVR).Namespace(b.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.Instance{}, domain.NoSuchInstance(name)
		}
		return domain.Instance{}, fmt.Errorf("get Redis CR: %w", err)
	}
	return b.toDomain(ctx, redis), nil
}

func (b *Backend) ListInstances(ctx context.Context, opt domain.ListInstancesOptions) (domain.ListInstancesResult, error) {
	list, err := b.dyn.Resource(redisGVR).Namespace(b.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return domain.ListInstancesResult{}, fmt.Errorf("list Redis CRs: %w", err)
	}
	res := domain.ListInstancesResult{}
	for i := range list.Items {
		r := &list.Items[i]
		if opt.Prefix != "" && !strings.HasPrefix(r.GetName(), opt.Prefix) {
			continue
		}
		res.Instances = append(res.Instances, b.toDomain(ctx, r))
		if opt.MaxResults > 0 && len(res.Instances) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) ModifyInstance(ctx context.Context, name string, opt domain.ModifyInstanceOptions) error {
	if opt.AuthToken != "" {
		secret, err := b.core.CoreV1().Secrets(b.namespace).Get(ctx, name+"-shim-auth", metav1.GetOptions{})
		if err == nil {
			if secret.StringData == nil {
				secret.StringData = map[string]string{}
			}
			secret.StringData["password"] = opt.AuthToken
			_, _ = b.core.CoreV1().Secrets(b.namespace).Update(ctx, secret, metav1.UpdateOptions{})
		}
	}
	return nil
}

func (b *Backend) RebootInstance(ctx context.Context, name string) error {
	// Bump an annotation to trigger a rolling restart.
	redis, err := b.dyn.Resource(redisGVR).Namespace(b.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.NoSuchInstance(name)
		}
		return err
	}
	anns := redis.GetAnnotations()
	if anns == nil {
		anns = map[string]string{}
	}
	anns["shim.cache/reloadedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	redis.SetAnnotations(anns)
	if _, err := b.dyn.Resource(redisGVR).Namespace(b.namespace).Update(ctx, redis, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("annotate Redis CR for restart: %w", err)
	}
	return nil
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func (b *Backend) toDomain(ctx context.Context, u *unstructured.Unstructured) domain.Instance {
	name := u.GetName()
	inst := domain.Instance{
		Name:      name,
		Status:    b.statusFor(ctx, u),
		CreatedAt: u.GetCreationTimestamp().Time,
	}
	if inst.Status == domain.StatusAvailable {
		// The operator creates a Service named `<redis-name>` (single
		// node deployment) or `<redis-name>-master` (Sentinel). Single
		// node is the intersection case.
		svc, err := b.core.CoreV1().Services(b.namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			inst.Connection = domain.Connection{
				Host: svc.Name + "." + svc.Namespace + ".svc.cluster.local",
				Port: 6379,
			}
		}
		// Auth token from the credentials Secret.
		if secret, err := b.core.CoreV1().Secrets(b.namespace).Get(ctx, name+"-shim-auth", metav1.GetOptions{}); err == nil {
			inst.Connection.AuthToken = string(secret.Data["password"])
		}
	}
	return inst
}

// statusFor reads the operator-managed StatefulSet to decide whether
// the Redis is available. The Redis CR's own .status fields differ
// across the various operators (Sentinel, Replication, single-node)
// and aren't reliably populated for the v1beta2 Redis kind; the
// StatefulSet's ReadyReplicas count is the most universal signal —
// it's what every operator topology lands on.
func (b *Backend) statusFor(ctx context.Context, u *unstructured.Unstructured) domain.Status {
	if u.GetDeletionTimestamp() != nil {
		return domain.StatusDeleting
	}
	name := u.GetName()
	ss, err := b.core.AppsV1().StatefulSets(b.namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil && ss.Status.ReadyReplicas > 0 {
		return domain.StatusAvailable
	}
	return domain.StatusCreating
}
