// Package inmem is an in-process functions backend used by the
// conformance harness as the always-on baseline.
package inmem

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/functions/domain"
)

type Backend struct {
	mu        sync.Mutex
	functions map[string]*functionState
	delay     time.Duration
}

type functionState struct {
	name           string
	image          string
	status         domain.Status
	environment    map[string]string
	memoryBytes    int64
	cpuMilliCores  int
	timeoutSeconds int
	role           string
	publish        bool
	createdAt      time.Time
}

func New() *Backend {
	return &Backend{
		functions: map[string]*functionState{},
		delay:     50 * time.Millisecond,
	}
}

var _ domain.Functions = (*Backend)(nil)

func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (b *Backend) toDomain(s *functionState) domain.Function {
	out := domain.Function{
		Name:           s.name,
		Image:          s.image,
		Status:         s.status,
		Environment:    copyMap(s.environment),
		MemoryBytes:    s.memoryBytes,
		CPUMilliCores:  s.cpuMilliCores,
		TimeoutSeconds: s.timeoutSeconds,
		Role:           s.role,
		Publish:        s.publish,
		CreatedAt:      s.createdAt,
	}
	if s.status == domain.StatusAvailable {
		out.Endpoint = domain.Endpoint{
			URL: "http://localhost:8080/" + s.name,
		}
	}
	return out
}

func (b *Backend) scheduleAvailable(name string) {
	go func() {
		time.Sleep(b.delay)
		b.mu.Lock()
		defer b.mu.Unlock()
		if s, ok := b.functions[name]; ok && s.status != domain.StatusDeleting {
			s.status = domain.StatusAvailable
		}
	}()
}

func (b *Backend) CreateFunction(ctx context.Context, name string, opt domain.CreateFunctionOptions) (domain.Function, error) {
	if opt.Image == "" {
		return domain.Function{}, domain.InvalidArgument("Image is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.functions[name]; ok {
		return domain.Function{}, domain.FunctionAlreadyExists(name)
	}
	s := &functionState{
		name:           name,
		image:          opt.Image,
		status:         domain.StatusCreating,
		environment:    copyMap(opt.Environment),
		memoryBytes:    opt.MemoryBytes,
		cpuMilliCores:  opt.CPUMilliCores,
		timeoutSeconds: opt.TimeoutSeconds,
		role:           opt.Role,
		publish:        opt.Publish,
		createdAt:      time.Now().UTC(),
	}
	b.functions[name] = s
	b.scheduleAvailable(name)
	return b.toDomain(s), nil
}

func (b *Backend) DeleteFunction(ctx context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.functions[name]
	if !ok {
		return domain.NoSuchFunction(name)
	}
	s.status = domain.StatusDeleting
	go func() {
		time.Sleep(b.delay)
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.functions, name)
	}()
	return nil
}

func (b *Backend) DescribeFunction(ctx context.Context, name string) (domain.Function, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.functions[name]
	if !ok {
		return domain.Function{}, domain.NoSuchFunction(name)
	}
	return b.toDomain(s), nil
}

func (b *Backend) ListFunctions(ctx context.Context, opt domain.ListFunctionsOptions) (domain.ListFunctionsResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	names := make([]string, 0, len(b.functions))
	for n := range b.functions {
		if opt.Prefix != "" && !strings.HasPrefix(n, opt.Prefix) {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	res := domain.ListFunctionsResult{}
	for _, n := range names {
		res.Functions = append(res.Functions, b.toDomain(b.functions[n]))
		if opt.MaxResults > 0 && len(res.Functions) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) UpdateFunction(ctx context.Context, name string, opt domain.UpdateFunctionOptions) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.functions[name]
	if !ok {
		return domain.NoSuchFunction(name)
	}
	if opt.Image != "" {
		s.image = opt.Image
	}
	if opt.Environment != nil {
		s.environment = copyMap(opt.Environment)
	}
	if opt.MemoryBytes > 0 {
		s.memoryBytes = opt.MemoryBytes
	}
	if opt.CPUMilliCores > 0 {
		s.cpuMilliCores = opt.CPUMilliCores
	}
	if opt.TimeoutSeconds > 0 {
		s.timeoutSeconds = opt.TimeoutSeconds
	}
	s.status = domain.StatusUpdating
	go func() {
		time.Sleep(b.delay)
		b.mu.Lock()
		defer b.mu.Unlock()
		if s2, ok := b.functions[name]; ok && s2.status == domain.StatusUpdating {
			s2.status = domain.StatusAvailable
		}
	}()
	return nil
}
