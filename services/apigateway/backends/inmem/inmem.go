// Package inmem is an in-process API Gateway backend.
package inmem

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
)

type Backend struct {
	mu       sync.Mutex
	gateways map[string]*gatewayState
	delay    time.Duration
}

type gatewayState struct {
	name      string
	status    domain.Status
	routes    []domain.Route
	createdAt time.Time
}

func New() *Backend {
	return &Backend{
		gateways: map[string]*gatewayState{},
		delay:    50 * time.Millisecond,
	}
}

var _ domain.APIGateway = (*Backend)(nil)

func cloneRoutes(r []domain.Route) []domain.Route {
	if r == nil {
		return nil
	}
	out := make([]domain.Route, len(r))
	copy(out, r)
	return out
}

func (b *Backend) toDomain(s *gatewayState) domain.Gateway {
	out := domain.Gateway{
		Name:      s.name,
		Status:    s.status,
		Routes:    cloneRoutes(s.routes),
		CreatedAt: s.createdAt,
	}
	if s.status == domain.StatusAvailable {
		out.Endpoint = domain.Endpoint{
			URL: "http://localhost:9700/" + s.name,
		}
	}
	return out
}

func (b *Backend) scheduleAvailable(name string) {
	go func() {
		time.Sleep(b.delay)
		b.mu.Lock()
		defer b.mu.Unlock()
		if s, ok := b.gateways[name]; ok && s.status != domain.StatusDeleting {
			s.status = domain.StatusAvailable
		}
	}()
}

func (b *Backend) CreateGateway(ctx context.Context, name string, opt domain.CreateGatewayOptions) (domain.Gateway, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.gateways[name]; ok {
		return domain.Gateway{}, domain.GatewayAlreadyExists(name)
	}
	s := &gatewayState{
		name:      name,
		status:    domain.StatusCreating,
		routes:    cloneRoutes(opt.Routes),
		createdAt: time.Now().UTC(),
	}
	b.gateways[name] = s
	b.scheduleAvailable(name)
	return b.toDomain(s), nil
}

func (b *Backend) DeleteGateway(ctx context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.gateways[name]
	if !ok {
		return domain.NoSuchGateway(name)
	}
	s.status = domain.StatusDeleting
	go func() {
		time.Sleep(b.delay)
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.gateways, name)
	}()
	return nil
}

func (b *Backend) DescribeGateway(ctx context.Context, name string) (domain.Gateway, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.gateways[name]
	if !ok {
		return domain.Gateway{}, domain.NoSuchGateway(name)
	}
	return b.toDomain(s), nil
}

func (b *Backend) ListGateways(ctx context.Context, opt domain.ListGatewaysOptions) (domain.ListGatewaysResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	names := make([]string, 0, len(b.gateways))
	for n := range b.gateways {
		if opt.Prefix != "" && !strings.HasPrefix(n, opt.Prefix) {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	res := domain.ListGatewaysResult{}
	for _, n := range names {
		res.Gateways = append(res.Gateways, b.toDomain(b.gateways[n]))
		if opt.MaxResults > 0 && len(res.Gateways) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) DeployGateway(ctx context.Context, name string, opt domain.DeployGatewayOptions) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.gateways[name]
	if !ok {
		return domain.NoSuchGateway(name)
	}
	// Atomic swap of the routing table.
	s.routes = cloneRoutes(opt.Routes)
	s.status = domain.StatusUpdating
	go func() {
		time.Sleep(b.delay)
		b.mu.Lock()
		defer b.mu.Unlock()
		if s2, ok := b.gateways[name]; ok && s2.status == domain.StatusUpdating {
			s2.status = domain.StatusAvailable
		}
	}()
	return nil
}
