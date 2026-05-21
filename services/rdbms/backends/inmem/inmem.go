// Package inmem is an in-process rdbms backend used by the
// conformance harness as the always-on baseline. Not a production
// backend — `shim rdbms -backend=inmem` is for tests only.
//
// The async lifecycle is faked: CreateInstance returns immediately
// with Status=Creating; a background goroutine flips Status to
// Available after a short delay (default 50ms). The same delay
// applies to Modify, Reboot, and snapshot Create. Tests can poll
// DescribeInstance with a small budget to observe the transition.
//
// Connection blocks point at a dummy localhost host on a per-engine
// default port (5432 for Postgres, 3306 for MySQL). The inmem
// backend doesn't run a real database — psql connectivity tests
// must use the CloudNativePG backend instead.
package inmem

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
)

type Backend struct {
	mu        sync.Mutex
	instances map[string]*instanceState
	snapshots map[string]*snapshotState
	delay     time.Duration
}

type instanceState struct {
	name               string
	engine             domain.Engine
	engineVersion      string
	status             domain.Status
	allocatedStorageGB int
	instanceClass      string
	region             string
	masterUsername     string
	masterPassword     string
	createdAt          time.Time
}

type snapshotState struct {
	id            string
	instance      string
	engine        domain.Engine
	engineVersion string
	status        domain.Status
	createdAt     time.Time
}

func New() *Backend {
	return &Backend{
		instances: map[string]*instanceState{},
		snapshots: map[string]*snapshotState{},
		delay:     50 * time.Millisecond,
	}
}

var _ domain.RDBMS = (*Backend)(nil)

func newPassword() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func portFor(e domain.Engine) int {
	switch e {
	case domain.EngineMySQL:
		return 3306
	default:
		return 5432
	}
}

func defaultDatabaseName(e domain.Engine) string {
	switch e {
	case domain.EngineMySQL:
		return "mysql"
	default:
		return "postgres"
	}
}

// scheduleAvailable flips the instance to Available after b.delay.
// Tests can keep delay small; never zero (otherwise the test can't
// observe the Creating state).
func (b *Backend) scheduleAvailable(name string) {
	go func() {
		time.Sleep(b.delay)
		b.mu.Lock()
		defer b.mu.Unlock()
		if s, ok := b.instances[name]; ok && s.status != domain.StatusDeleting {
			s.status = domain.StatusAvailable
		}
	}()
}

func (b *Backend) scheduleSnapshotAvailable(id string) {
	go func() {
		time.Sleep(b.delay)
		b.mu.Lock()
		defer b.mu.Unlock()
		if s, ok := b.snapshots[id]; ok {
			s.status = domain.StatusAvailable
		}
	}()
}

func (b *Backend) instanceToDomain(s *instanceState) domain.Instance {
	out := domain.Instance{
		Name:               s.name,
		Engine:             s.engine,
		EngineVersion:      s.engineVersion,
		Status:             s.status,
		AllocatedStorageGB: s.allocatedStorageGB,
		InstanceClass:      s.instanceClass,
		Region:             s.region,
		CreatedAt:          s.createdAt,
	}
	if s.status == domain.StatusAvailable {
		out.Connection = domain.Connection{
			Host:           "localhost",
			Port:           portFor(s.engine),
			MasterUsername: s.masterUsername,
			DatabaseName:   defaultDatabaseName(s.engine),
			EngineVersion:  s.engineVersion,
		}
	}
	return out
}

func (b *Backend) snapshotToDomain(s *snapshotState) domain.Snapshot {
	return domain.Snapshot{
		ID:            s.id,
		Instance:      s.instance,
		Engine:        s.engine,
		EngineVersion: s.engineVersion,
		Status:        s.status,
		CreatedAt:     s.createdAt,
	}
}

// ----------------------------------------------------------------------
// Instances
// ----------------------------------------------------------------------

func (b *Backend) CreateInstance(ctx context.Context, name string, opt domain.CreateInstanceOptions) (domain.CreateInstanceResult, error) {
	if opt.Engine == domain.EngineUnknown {
		return domain.CreateInstanceResult{}, domain.UnsupportedEngine("engine is required")
	}
	if opt.MasterUsername == "" {
		return domain.CreateInstanceResult{}, domain.InvalidArgument("MasterUsername is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.instances[name]; ok {
		return domain.CreateInstanceResult{}, domain.InstanceAlreadyExists(name)
	}
	password := opt.MasterPassword
	revealed := ""
	if password == "" {
		password = newPassword()
		revealed = password
	}
	now := time.Now().UTC()
	s := &instanceState{
		name:               name,
		engine:             opt.Engine,
		engineVersion:      opt.EngineVersion,
		status:             domain.StatusCreating,
		allocatedStorageGB: opt.AllocatedStorageGB,
		instanceClass:      opt.InstanceClass,
		region:             opt.Region,
		masterUsername:     opt.MasterUsername,
		masterPassword:     password,
		createdAt:          now,
	}
	b.instances[name] = s
	b.scheduleAvailable(name)
	return domain.CreateInstanceResult{
		Instance:       b.instanceToDomain(s),
		MasterPassword: revealed,
	}, nil
}

func (b *Backend) DeleteInstance(ctx context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.instances[name]
	if !ok {
		return domain.NoSuchInstance(name)
	}
	s.status = domain.StatusDeleting
	go func() {
		time.Sleep(b.delay)
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.instances, name)
	}()
	return nil
}

func (b *Backend) DescribeInstance(ctx context.Context, name string) (domain.Instance, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.instances[name]
	if !ok {
		return domain.Instance{}, domain.NoSuchInstance(name)
	}
	return b.instanceToDomain(s), nil
}

func (b *Backend) ListInstances(ctx context.Context, opt domain.ListInstancesOptions) (domain.ListInstancesResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	names := make([]string, 0, len(b.instances))
	for n := range b.instances {
		if opt.Prefix != "" && !strings.HasPrefix(n, opt.Prefix) {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	res := domain.ListInstancesResult{}
	for _, n := range names {
		res.Instances = append(res.Instances, b.instanceToDomain(b.instances[n]))
		if opt.MaxResults > 0 && len(res.Instances) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) ModifyInstance(ctx context.Context, name string, opt domain.ModifyInstanceOptions) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.instances[name]
	if !ok {
		return domain.NoSuchInstance(name)
	}
	if opt.AllocatedStorageGB > 0 {
		s.allocatedStorageGB = opt.AllocatedStorageGB
	}
	if opt.InstanceClass != "" {
		s.instanceClass = opt.InstanceClass
	}
	if opt.MasterPassword != "" {
		s.masterPassword = opt.MasterPassword
	}
	s.status = domain.StatusModifying
	go func() {
		time.Sleep(b.delay)
		b.mu.Lock()
		defer b.mu.Unlock()
		if s2, ok := b.instances[name]; ok && s2.status == domain.StatusModifying {
			s2.status = domain.StatusAvailable
		}
	}()
	return nil
}

func (b *Backend) RebootInstance(ctx context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.instances[name]
	if !ok {
		return domain.NoSuchInstance(name)
	}
	s.status = domain.StatusRebooting
	go func() {
		time.Sleep(b.delay)
		b.mu.Lock()
		defer b.mu.Unlock()
		if s2, ok := b.instances[name]; ok && s2.status == domain.StatusRebooting {
			s2.status = domain.StatusAvailable
		}
	}()
	return nil
}

// ----------------------------------------------------------------------
// Snapshots
// ----------------------------------------------------------------------

func (b *Backend) CreateSnapshot(ctx context.Context, instance, snapshotID string) (domain.Snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	inst, ok := b.instances[instance]
	if !ok {
		return domain.Snapshot{}, domain.NoSuchInstance(instance)
	}
	if _, ok := b.snapshots[snapshotID]; ok {
		return domain.Snapshot{}, domain.SnapshotAlreadyExists(snapshotID)
	}
	s := &snapshotState{
		id:            snapshotID,
		instance:      instance,
		engine:        inst.engine,
		engineVersion: inst.engineVersion,
		status:        domain.StatusCreating,
		createdAt:     time.Now().UTC(),
	}
	b.snapshots[snapshotID] = s
	b.scheduleSnapshotAvailable(snapshotID)
	return b.snapshotToDomain(s), nil
}

func (b *Backend) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.snapshots[snapshotID]; !ok {
		return domain.NoSuchSnapshot(snapshotID)
	}
	delete(b.snapshots, snapshotID)
	return nil
}

func (b *Backend) DescribeSnapshot(ctx context.Context, snapshotID string) (domain.Snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.snapshots[snapshotID]
	if !ok {
		return domain.Snapshot{}, domain.NoSuchSnapshot(snapshotID)
	}
	return b.snapshotToDomain(s), nil
}

func (b *Backend) ListSnapshots(ctx context.Context, opt domain.ListSnapshotsOptions) (domain.ListSnapshotsResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make([]string, 0, len(b.snapshots))
	for id, s := range b.snapshots {
		if opt.Instance != "" && s.instance != opt.Instance {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	res := domain.ListSnapshotsResult{}
	for _, id := range ids {
		res.Snapshots = append(res.Snapshots, b.snapshotToDomain(b.snapshots[id]))
		if opt.MaxResults > 0 && len(res.Snapshots) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) RestoreFromSnapshot(ctx context.Context, snapshotID, newInstanceName string) (domain.CreateInstanceResult, error) {
	b.mu.Lock()
	snap, ok := b.snapshots[snapshotID]
	b.mu.Unlock()
	if !ok {
		return domain.CreateInstanceResult{}, domain.NoSuchSnapshot(snapshotID)
	}
	return b.CreateInstance(ctx, newInstanceName, domain.CreateInstanceOptions{
		Engine:         snap.engine,
		EngineVersion:  snap.engineVersion,
		MasterUsername: "shimadmin",
	})
}
