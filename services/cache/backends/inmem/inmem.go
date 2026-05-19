// Package inmem is an in-process cache backend used by the
// conformance harness as the always-on baseline. Not a production
// backend — `shim cache -backend=inmem` is for tests only.
//
// Async lifecycle fake mirrors Phase 5 rdbms inmem: CreateInstance
// returns Status=Creating; a background goroutine flips to Available
// after 50ms (configurable).
package inmem

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/cache/domain"
)

type Backend struct {
	mu        sync.Mutex
	instances map[string]*instanceState
	delay     time.Duration
}

type instanceState struct {
	name          string
	engineVersion string
	nodeType      string
	status        domain.Status
	authToken     string
	createdAt     time.Time
}

func New() *Backend {
	return &Backend{
		instances: map[string]*instanceState{},
		delay:     50 * time.Millisecond,
	}
}

var _ domain.Cache = (*Backend)(nil)

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

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

func (b *Backend) toDomain(s *instanceState) domain.Instance {
	out := domain.Instance{
		Name:          s.name,
		EngineVersion: s.engineVersion,
		NodeType:      s.nodeType,
		Status:        s.status,
		CreatedAt:     s.createdAt,
	}
	if s.status == domain.StatusAvailable {
		out.Connection = domain.Connection{
			Host:          "localhost",
			Port:          6379,
			AuthToken:     s.authToken,
			EngineVersion: s.engineVersion,
		}
	}
	return out
}

func (b *Backend) CreateInstance(ctx context.Context, name string, opt domain.CreateInstanceOptions) (domain.CreateInstanceResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.instances[name]; ok {
		return domain.CreateInstanceResult{}, domain.InstanceAlreadyExists(name)
	}
	token := opt.AuthToken
	revealed := ""
	if token == "" {
		token = newToken()
		revealed = token
	}
	now := time.Now().UTC()
	s := &instanceState{
		name:          name,
		engineVersion: opt.EngineVersion,
		nodeType:      opt.NodeType,
		status:        domain.StatusCreating,
		authToken:     token,
		createdAt:     now,
	}
	b.instances[name] = s
	b.scheduleAvailable(name)
	return domain.CreateInstanceResult{
		Instance:  b.toDomain(s),
		AuthToken: revealed,
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
	return b.toDomain(s), nil
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
		res.Instances = append(res.Instances, b.toDomain(b.instances[n]))
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
	if opt.NodeType != "" {
		s.nodeType = opt.NodeType
	}
	if opt.AuthToken != "" {
		s.authToken = opt.AuthToken
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
