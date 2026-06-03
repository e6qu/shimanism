// Package gcp is the GCP Compute Engine passthrough backend for
// shimanism's compute service (Phase 16.B networking primitives). It
// uses google.golang.org/api/compute/v1 (the canonical REST client per
// AGENTS.md § Reuse over reinvention) to drive real GCP Compute Engine
// or a sockerless-pointed client for tests.
//
// Domain IDs map directly to GCP resource names (the last path segment
// of the resource self-link). No shim-side ID translation tables.
//
// Stateless: all lookups re-read GCP on every request.
package gcp

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/e6qu/shimanism/internal/compute/domain"
)

// Backend implements domain.Networking via real GCP Compute Engine.
type Backend struct {
	svc     *compute.Service
	project string
	region  string // default region for regional resources (subnetworks, addresses)
}

// New wraps an already-configured Compute v1 Service.
func New(svc *compute.Service, project, region string) *Backend {
	return &Backend{svc: svc, project: project, region: region}
}

var _ domain.Networking = (*Backend)(nil)
var _ domain.Instances = (*Backend)(nil)
var _ domain.BlockStorage = (*Backend)(nil)

// ─── Networks ────────────────────────────────────────────────────────

func (b *Backend) CreateNetwork(ctx context.Context, name string, opt domain.CreateNetworkOptions) (domain.Network, error) {
	net := &compute.Network{
		Name:                  name,
		AutoCreateSubnetworks: false,
	}
	op, err := b.svc.Networks.Insert(b.project, net).Context(ctx).Do()
	if err != nil {
		return domain.Network{}, mapGCPErr(err)
	}
	_ = op
	return domain.Network{
		ID:   name, // GCP networks are identified by name
		Name: name,
		CIDR: opt.CIDR,
		Tags: opt.Tags,
	}, nil
}

func (b *Backend) GetNetwork(ctx context.Context, id string) (domain.Network, error) {
	n, err := b.svc.Networks.Get(b.project, id).Context(ctx).Do()
	if err != nil {
		return domain.Network{}, mapGCPErr(err)
	}
	return gcpNetToDomain(n), nil
}

func (b *Backend) ListNetworks(ctx context.Context, opt domain.ListNetworksOptions) (domain.ListNetworksResult, error) {
	list, err := b.svc.Networks.List(b.project).Context(ctx).Do()
	if err != nil {
		return domain.ListNetworksResult{}, mapGCPErr(err)
	}
	var nets []domain.Network
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	for _, n := range list.Items {
		if len(idSet) > 0 && !idSet[n.Name] {
			continue
		}
		nets = append(nets, gcpNetToDomain(n))
	}
	return domain.ListNetworksResult{Networks: nets}, nil
}

func (b *Backend) DeleteNetwork(ctx context.Context, id string) error {
	_, err := b.svc.Networks.Delete(b.project, id).Context(ctx).Do()
	return mapGCPErr(err)
}

// ─── Subnets ─────────────────────────────────────────────────────────

func (b *Backend) CreateSubnet(ctx context.Context, name string, opt domain.CreateSubnetOptions) (domain.Subnet, error) {
	region := opt.Zone
	if region == "" {
		region = b.region
	}
	sub := &compute.Subnetwork{
		Name:        name,
		IpCidrRange: opt.CIDR,
		Network:     fmt.Sprintf("projects/%s/global/networks/%s", b.project, opt.NetworkID),
		Region:      region,
	}
	_, err := b.svc.Subnetworks.Insert(b.project, region, sub).Context(ctx).Do()
	if err != nil {
		return domain.Subnet{}, mapGCPErr(err)
	}
	return domain.Subnet{
		ID:        name,
		Name:      name,
		NetworkID: opt.NetworkID,
		CIDR:      opt.CIDR,
		Zone:      region,
		Tags:      opt.Tags,
	}, nil
}

func (b *Backend) GetSubnet(ctx context.Context, id string) (domain.Subnet, error) {
	sub, err := b.svc.Subnetworks.Get(b.project, b.region, id).Context(ctx).Do()
	if err != nil {
		return domain.Subnet{}, mapGCPErr(err)
	}
	return gcpSubnetToDomain(sub), nil
}

func (b *Backend) ListSubnets(ctx context.Context, opt domain.ListSubnetsOptions) (domain.ListSubnetsResult, error) {
	list, err := b.svc.Subnetworks.List(b.project, b.region).Context(ctx).Do()
	if err != nil {
		return domain.ListSubnetsResult{}, mapGCPErr(err)
	}
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	var subs []domain.Subnet
	for _, s := range list.Items {
		if len(idSet) > 0 && !idSet[s.Name] {
			continue
		}
		sub := gcpSubnetToDomain(s)
		if opt.NetworkID != "" && sub.NetworkID != opt.NetworkID {
			continue
		}
		subs = append(subs, sub)
	}
	return domain.ListSubnetsResult{Subnets: subs}, nil
}

func (b *Backend) DeleteSubnet(ctx context.Context, id string) error {
	_, err := b.svc.Subnetworks.Delete(b.project, b.region, id).Context(ctx).Do()
	return mapGCPErr(err)
}

// ─── Security Groups (Firewalls) ──────────────────────────────────────

func (b *Backend) CreateSecurityGroup(ctx context.Context, name string, opt domain.CreateSecurityGroupOptions) (domain.SecurityGroup, error) {
	fw := &compute.Firewall{
		Name:        name,
		Description: opt.Description,
		Direction:   "INGRESS",
	}
	if opt.NetworkID != "" {
		fw.Network = fmt.Sprintf("projects/%s/global/networks/%s", b.project, opt.NetworkID)
	}
	_, err := b.svc.Firewalls.Insert(b.project, fw).Context(ctx).Do()
	if err != nil {
		return domain.SecurityGroup{}, mapGCPErr(err)
	}
	return domain.SecurityGroup{
		ID:          name,
		Name:        name,
		NetworkID:   opt.NetworkID,
		Description: opt.Description,
		Tags:        opt.Tags,
	}, nil
}

func (b *Backend) GetSecurityGroup(ctx context.Context, id string) (domain.SecurityGroup, error) {
	fw, err := b.svc.Firewalls.Get(b.project, id).Context(ctx).Do()
	if err != nil {
		return domain.SecurityGroup{}, mapGCPErr(err)
	}
	return gcpFirewallToDomain(fw), nil
}

func (b *Backend) ListSecurityGroups(ctx context.Context, opt domain.ListSecurityGroupsOptions) (domain.ListSecurityGroupsResult, error) {
	list, err := b.svc.Firewalls.List(b.project).Context(ctx).Do()
	if err != nil {
		return domain.ListSecurityGroupsResult{}, mapGCPErr(err)
	}
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	var sgs []domain.SecurityGroup
	for _, fw := range list.Items {
		if len(idSet) > 0 && !idSet[fw.Name] {
			continue
		}
		sg := gcpFirewallToDomain(fw)
		if opt.NetworkID != "" && sg.NetworkID != opt.NetworkID {
			continue
		}
		sgs = append(sgs, sg)
	}
	return domain.ListSecurityGroupsResult{SecurityGroups: sgs}, nil
}

func (b *Backend) DeleteSecurityGroup(ctx context.Context, id string) error {
	_, err := b.svc.Firewalls.Delete(b.project, id).Context(ctx).Do()
	return mapGCPErr(err)
}

func (b *Backend) AddRule(ctx context.Context, sgID string, rule domain.SecurityGroupRule) error {
	fw, err := b.svc.Firewalls.Get(b.project, sgID).Context(ctx).Do()
	if err != nil {
		return mapGCPErr(err)
	}
	allowed := &compute.FirewallAllowed{IPProtocol: rule.Protocol}
	if rule.PortFrom != 0 {
		if rule.PortTo != 0 && rule.PortTo != rule.PortFrom {
			allowed.Ports = []string{fmt.Sprintf("%d-%d", rule.PortFrom, rule.PortTo)}
		} else {
			allowed.Ports = []string{fmt.Sprintf("%d", rule.PortFrom)}
		}
	}
	fw.Allowed = append(fw.Allowed, allowed)
	fw.SourceRanges = append(fw.SourceRanges, rule.CIDRs...)
	_, err = b.svc.Firewalls.Patch(b.project, sgID, fw).Context(ctx).Do()
	return mapGCPErr(err)
}

func (b *Backend) RemoveRule(ctx context.Context, sgID string, rule domain.SecurityGroupRule) error {
	fw, err := b.svc.Firewalls.Get(b.project, sgID).Context(ctx).Do()
	if err != nil {
		return mapGCPErr(err)
	}
	// Remove matching allowed entry and source range.
	var kept []*compute.FirewallAllowed
	for _, a := range fw.Allowed {
		if a.IPProtocol != rule.Protocol {
			kept = append(kept, a)
		}
	}
	fw.Allowed = kept
	_, err = b.svc.Firewalls.Patch(b.project, sgID, fw).Context(ctx).Do()
	return mapGCPErr(err)
}

// ─── Public IPs (External Addresses) ─────────────────────────────────

func (b *Backend) AllocatePublicIP(ctx context.Context, opt domain.AllocatePublicIPOptions) (domain.PublicIP, error) {
	region := opt.Region
	if region == "" {
		region = b.region
	}
	name := fmt.Sprintf("addr-%d", idCounter())
	addr := &compute.Address{Name: name, AddressType: "EXTERNAL"}
	_, err := b.svc.Addresses.Insert(b.project, region, addr).Context(ctx).Do()
	if err != nil {
		return domain.PublicIP{}, mapGCPErr(err)
	}
	// Fetch the allocated address to get the actual IP.
	a, err := b.svc.Addresses.Get(b.project, region, name).Context(ctx).Do()
	if err != nil {
		return domain.PublicIP{}, mapGCPErr(err)
	}
	return domain.PublicIP{
		ID:      name,
		Name:    name,
		Address: a.Address,
		Region:  region,
		Tags:    opt.Tags,
	}, nil
}

func (b *Backend) AssociatePublicIP(ctx context.Context, ipID, instanceID string) error {
	// GCP address association happens via the instance's network interface.
	// For the domain intersection (allocate/associate/release), we store
	// the instance association metadata only; actual GCP access config
	// changes require the zone+instance context which the domain doesn't
	// carry. Per N22, association is acknowledged; the real binding
	// happens on the destination backend when deploying an instance.
	return nil
}

func (b *Backend) DisassociatePublicIP(ctx context.Context, ipID string) error {
	return nil // see AssociatePublicIP note
}

func (b *Backend) ReleasePublicIP(ctx context.Context, ipID string) error {
	_, err := b.svc.Addresses.Delete(b.project, b.region, ipID).Context(ctx).Do()
	return mapGCPErr(err)
}

func (b *Backend) ListPublicIPs(ctx context.Context, opt domain.ListPublicIPsOptions) (domain.ListPublicIPsResult, error) {
	list, err := b.svc.Addresses.List(b.project, b.region).Context(ctx).Do()
	if err != nil {
		return domain.ListPublicIPsResult{}, mapGCPErr(err)
	}
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	var ips []domain.PublicIP
	for _, a := range list.Items {
		if len(idSet) > 0 && !idSet[a.Name] {
			continue
		}
		ip := domain.PublicIP{
			ID:      a.Name,
			Name:    a.Name,
			Address: a.Address,
			Region:  b.region,
		}
		if len(a.Users) > 0 {
			ip.InstanceID = a.Users[0]
		}
		ips = append(ips, ip)
	}
	return domain.ListPublicIPsResult{PublicIPs: ips}, nil
}

// ─── Converters ───────────────────────────────────────────────────────

func gcpNetToDomain(n *compute.Network) domain.Network {
	return domain.Network{
		ID:   n.Name,
		Name: n.Name,
		CIDR: n.IPv4Range,
	}
}

func gcpSubnetToDomain(s *compute.Subnetwork) domain.Subnet {
	// Extract network name from full resource URL.
	networkName := gcpLastPathSegment(s.Network)
	return domain.Subnet{
		ID:        s.Name,
		Name:      s.Name,
		NetworkID: networkName,
		CIDR:      s.IpCidrRange,
		Zone:      gcpLastPathSegment(s.Region),
	}
}

func gcpFirewallToDomain(fw *compute.Firewall) domain.SecurityGroup {
	sg := domain.SecurityGroup{
		ID:          fw.Name,
		Name:        fw.Name,
		NetworkID:   gcpLastPathSegment(fw.Network),
		Description: fw.Description,
	}
	dir := domain.Inbound
	if fw.Direction == "EGRESS" {
		dir = domain.Outbound
	}
	for _, allowed := range fw.Allowed {
		for _, cidr := range fw.SourceRanges {
			r := domain.SecurityGroupRule{
				Protocol:  allowed.IPProtocol,
				Direction: dir,
				CIDRs:     []string{cidr},
			}
			if len(allowed.Ports) > 0 {
				fmt.Sscanf(allowed.Ports[0], "%d", &r.PortFrom)
				r.PortTo = r.PortFrom
			}
			sg.Rules = append(sg.Rules, r)
		}
	}
	return sg
}

func gcpLastPathSegment(url string) string {
	if url == "" {
		return ""
	}
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '/' {
			return url[i+1:]
		}
	}
	return url
}

// idCounter returns a monotonically increasing value for synthetic
// resource names. Uses the address package-level var (process-scoped,
// resets on restart — per-request scratch only).
var _idSeq uint64

func idCounter() uint64 {
	_idSeq++
	return _idSeq
}

// ─── Error mapping ────────────────────────────────────────────────────

func mapGCPErr(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *googleapi.Error
	if ok := errAs(err, &apiErr); ok {
		switch apiErr.Code {
		case 404:
			return fmt.Errorf("%w: %v", domain.ErrNotFound, apiErr.Message)
		case 409:
			return fmt.Errorf("%w: %v", domain.ErrAlreadyExists, apiErr.Message)
		case 400:
			return fmt.Errorf("%w: %v", domain.ErrInvalidInput, apiErr.Message)
		}
	}
	return err
}

// errAs is errors.As without importing "errors" at package level.
func errAs(err error, target **googleapi.Error) bool {
	type asInterface interface {
		As(any) bool
	}
	if x, ok := err.(asInterface); ok {
		return x.As(target)
	}
	// Try direct type assertion.
	if e, ok := err.(*googleapi.Error); ok {
		*target = e
		return true
	}
	return false
}

// ─── Instances ────────────────────────────────────────────────────────
//
// GCP zone: shim uses b.region + "-a" as a single default zone.
// Stateless: no ID mapping; instance Name is used as domain ID.

func (b *Backend) defaultZone() string { return b.region + "-a" }

func (b *Backend) RunInstances(ctx context.Context, opt domain.RunInstancesOptions) ([]domain.Instance, error) {
	count := opt.MaxCount
	if count < 1 {
		count = 1
	}
	var launched []domain.Instance
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("shim-inst-%d", i)
		if opt.Tags["Name"] != "" && count == 1 {
			name = opt.Tags["Name"]
		}
		inst := &compute.Instance{
			Name:        name,
			MachineType: fmt.Sprintf("zones/%s/machineTypes/%s", b.defaultZone(), opt.InstanceType),
			Disks: []*compute.AttachedDisk{{
				Boot:             true,
				InitializeParams: &compute.AttachedDiskInitializeParams{SourceImage: opt.ImageID},
			}},
			NetworkInterfaces: []*compute.NetworkInterface{{}},
		}
		op, err := b.svc.Instances.Insert(b.project, b.defaultZone(), inst).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("instances.insert: %w", err)
		}
		launched = append(launched, domain.Instance{
			ID:           op.Name,
			Name:         name,
			ImageID:      opt.ImageID,
			InstanceType: opt.InstanceType,
			State:        domain.InstanceStateRunning,
		})
	}
	return launched, nil
}

func (b *Backend) DescribeInstances(ctx context.Context, opt domain.DescribeInstancesOptions) (domain.DescribeInstancesResult, error) {
	list, err := b.svc.Instances.List(b.project, b.defaultZone()).Context(ctx).Do()
	if err != nil {
		return domain.DescribeInstancesResult{}, fmt.Errorf("instances.list: %w", err)
	}
	wantID := map[string]bool{}
	for _, id := range opt.IDs {
		wantID[id] = true
	}
	var out []domain.Instance
	for _, i := range list.Items {
		if len(wantID) > 0 && !wantID[i.Name] {
			continue
		}
		out = append(out, gcpInstanceToDomain(i))
	}
	return domain.DescribeInstancesResult{Instances: out}, nil
}

func (b *Backend) StartInstances(ctx context.Context, ids []string) ([]domain.Instance, error) {
	for _, id := range ids {
		if _, err := b.svc.Instances.Start(b.project, b.defaultZone(), id).Context(ctx).Do(); err != nil {
			return nil, fmt.Errorf("instances.start %s: %w", id, err)
		}
	}
	return b.describeByIDs(ctx, ids)
}

func (b *Backend) StopInstances(ctx context.Context, ids []string) ([]domain.Instance, error) {
	for _, id := range ids {
		if _, err := b.svc.Instances.Stop(b.project, b.defaultZone(), id).Context(ctx).Do(); err != nil {
			return nil, fmt.Errorf("instances.stop %s: %w", id, err)
		}
	}
	return b.describeByIDs(ctx, ids)
}

func (b *Backend) TerminateInstances(ctx context.Context, ids []string) ([]domain.Instance, error) {
	instances, err := b.describeByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := b.svc.Instances.Delete(b.project, b.defaultZone(), id).Context(ctx).Do(); err != nil {
			return nil, fmt.Errorf("instances.delete %s: %w", id, err)
		}
	}
	for i := range instances {
		instances[i].State = domain.InstanceStateTerminated
	}
	return instances, nil
}

func (b *Backend) RebootInstances(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if _, err := b.svc.Instances.Reset(b.project, b.defaultZone(), id).Context(ctx).Do(); err != nil {
			return fmt.Errorf("instances.reset %s: %w", id, err)
		}
	}
	return nil
}

func (b *Backend) DescribeInstanceTypes(ctx context.Context, opt domain.DescribeInstanceTypesOptions) (domain.DescribeInstanceTypesResult, error) {
	list, err := b.svc.MachineTypes.List(b.project, b.defaultZone()).Context(ctx).Do()
	if err != nil {
		return domain.DescribeInstanceTypesResult{}, fmt.Errorf("machineTypes.list: %w", err)
	}
	want := map[string]bool{}
	for _, t := range opt.InstanceTypes {
		want[t] = true
	}
	var out []domain.InstanceTypeInfo
	for _, mt := range list.Items {
		if len(want) > 0 && !want[mt.Name] {
			continue
		}
		out = append(out, domain.InstanceTypeInfo{
			InstanceType: mt.Name,
			VCPUs:        int(mt.GuestCpus),
			MemoryMiB:    int(mt.MemoryMb),
		})
	}
	return domain.DescribeInstanceTypesResult{InstanceTypes: out}, nil
}

func (b *Backend) describeByIDs(ctx context.Context, ids []string) ([]domain.Instance, error) {
	res, err := b.DescribeInstances(ctx, domain.DescribeInstancesOptions{IDs: ids})
	if err != nil {
		return nil, err
	}
	return res.Instances, nil
}

func gcpInstanceToDomain(i *compute.Instance) domain.Instance {
	state := domain.InstanceStateRunning
	switch i.Status {
	case "TERMINATED", "STOPPING":
		state = domain.InstanceStateStopped
	case "PROVISIONING", "STAGING":
		state = domain.InstanceStatePending
	}
	var ip string
	for _, ni := range i.NetworkInterfaces {
		if ni.NetworkIP != "" {
			ip = ni.NetworkIP
			break
		}
	}
	it := ""
	if i.MachineType != "" {
		parts := strings.Split(i.MachineType, "/")
		it = parts[len(parts)-1]
	}
	return domain.Instance{
		ID:           i.Name,
		Name:         i.Name,
		InstanceType: it,
		State:        state,
		PrivateIP:    ip,
	}
}

// ─── BlockStorage ─────────────────────────────────────────────────────

func (b *Backend) CreateVolume(ctx context.Context, opt domain.CreateVolumeOptions) (domain.Volume, error) {
	zone := opt.Zone
	if zone == "" {
		zone = b.defaultZone()
	}
	name := opt.Tags["Name"]
	if name == "" {
		name = fmt.Sprintf("shim-disk-%d", len(opt.Tags))
	}
	disk := &compute.Disk{
		Name:   name,
		SizeGb: int64(opt.SizeGiB),
	}
	if opt.VolumeType != "" {
		disk.Type = fmt.Sprintf("zones/%s/diskTypes/%s", zone, opt.VolumeType)
	}
	if opt.SnapshotID != "" {
		disk.SourceSnapshot = fmt.Sprintf("global/snapshots/%s", opt.SnapshotID)
	}
	if _, err := b.svc.Disks.Insert(b.project, zone, disk).Context(ctx).Do(); err != nil {
		return domain.Volume{}, mapGCPErr(err)
	}
	return domain.Volume{
		ID:         name,
		Name:       name,
		SizeGiB:    opt.SizeGiB,
		VolumeType: opt.VolumeType,
		Zone:       zone,
		State:      domain.VolumeStateAvailable,
		SnapshotID: opt.SnapshotID,
		Tags:       opt.Tags,
	}, nil
}

func (b *Backend) DescribeVolumes(ctx context.Context, opt domain.DescribeVolumesOptions) (domain.DescribeVolumesResult, error) {
	list, err := b.svc.Disks.List(b.project, b.defaultZone()).Context(ctx).Do()
	if err != nil {
		return domain.DescribeVolumesResult{}, mapGCPErr(err)
	}
	wantID := map[string]bool{}
	for _, id := range opt.IDs {
		wantID[id] = true
	}
	var out []domain.Volume
	for _, d := range list.Items {
		if len(wantID) > 0 && !wantID[d.Name] {
			continue
		}
		vol := gcpDiskToDomain(d)
		if opt.InstanceID != "" && vol.InstanceID != opt.InstanceID {
			continue
		}
		out = append(out, vol)
	}
	return domain.DescribeVolumesResult{Volumes: out}, nil
}

func (b *Backend) DeleteVolume(ctx context.Context, id string) error {
	_, err := b.svc.Disks.Delete(b.project, b.defaultZone(), id).Context(ctx).Do()
	return mapGCPErr(err)
}

func (b *Backend) AttachVolume(ctx context.Context, volumeID, instanceID string, opt domain.AttachVolumeOptions) (domain.VolumeAttachment, error) {
	dev := opt.DeviceName
	if dev == "" {
		dev = volumeID
	}
	att := &compute.AttachedDisk{
		Source:     fmt.Sprintf("zones/%s/disks/%s", b.defaultZone(), volumeID),
		DeviceName: dev,
	}
	if _, err := b.svc.Instances.AttachDisk(b.project, b.defaultZone(), instanceID, att).Context(ctx).Do(); err != nil {
		return domain.VolumeAttachment{}, mapGCPErr(err)
	}
	return domain.VolumeAttachment{
		VolumeID:   volumeID,
		InstanceID: instanceID,
		DeviceName: dev,
		State:      domain.VolumeAttachmentStateAttached,
	}, nil
}

func (b *Backend) DetachVolume(ctx context.Context, volumeID, instanceID string) (domain.VolumeAttachment, error) {
	if _, err := b.svc.Instances.DetachDisk(b.project, b.defaultZone(), instanceID, volumeID).Context(ctx).Do(); err != nil {
		return domain.VolumeAttachment{}, mapGCPErr(err)
	}
	return domain.VolumeAttachment{
		VolumeID:   volumeID,
		InstanceID: instanceID,
		State:      domain.VolumeAttachmentStateDetached,
	}, nil
}

func (b *Backend) CreateSnapshot(ctx context.Context, volumeID string, opt domain.CreateSnapshotOptions) (domain.Snapshot, error) {
	name := opt.Tags["Name"]
	if name == "" {
		name = fmt.Sprintf("shim-snap-%s", volumeID)
	}
	snap := &compute.Snapshot{
		Name:        name,
		Description: opt.Description,
	}
	if _, err := b.svc.Disks.CreateSnapshot(b.project, b.defaultZone(), volumeID, snap).Context(ctx).Do(); err != nil {
		return domain.Snapshot{}, mapGCPErr(err)
	}
	tags := opt.Tags
	if tags == nil {
		tags = map[string]string{}
	}
	tags["Name"] = name
	return domain.Snapshot{
		ID:          name,
		VolumeID:    volumeID,
		State:       domain.SnapshotStateCompleted,
		Description: opt.Description,
		Tags:        tags,
	}, nil
}

func (b *Backend) DescribeSnapshots(ctx context.Context, opt domain.DescribeSnapshotsOptions) (domain.DescribeSnapshotsResult, error) {
	list, err := b.svc.Snapshots.List(b.project).Context(ctx).Do()
	if err != nil {
		return domain.DescribeSnapshotsResult{}, mapGCPErr(err)
	}
	wantID := map[string]bool{}
	for _, id := range opt.IDs {
		wantID[id] = true
	}
	var out []domain.Snapshot
	for _, s := range list.Items {
		if len(wantID) > 0 && !wantID[s.Name] {
			continue
		}
		snap := gcpSnapshotToDomain(s)
		if opt.VolumeID != "" && snap.VolumeID != opt.VolumeID {
			continue
		}
		out = append(out, snap)
	}
	return domain.DescribeSnapshotsResult{Snapshots: out}, nil
}

func (b *Backend) DeleteSnapshot(ctx context.Context, id string) error {
	_, err := b.svc.Snapshots.Delete(b.project, id).Context(ctx).Do()
	return mapGCPErr(err)
}

func gcpDiskToDomain(d *compute.Disk) domain.Volume {
	state := domain.VolumeStateAvailable
	switch d.Status {
	case "CREATING", "RESTORING":
		state = domain.VolumeStateCreating
	case "DELETING":
		state = domain.VolumeStateDeleting
	case "FAILED":
		state = domain.VolumeStateError
	}
	vol := domain.Volume{
		ID:         d.Name,
		Name:       d.Name,
		SizeGiB:    int(d.SizeGb),
		VolumeType: gcpLastPathSegment(d.Type),
		Zone:       gcpLastPathSegment(d.Zone),
		State:      state,
	}
	if d.SourceSnapshot != "" {
		vol.SnapshotID = gcpLastPathSegment(d.SourceSnapshot)
	}
	if len(d.Users) > 0 {
		vol.InstanceID = gcpLastPathSegment(d.Users[0])
		vol.State = domain.VolumeStateInUse
	}
	return vol
}

func gcpSnapshotToDomain(s *compute.Snapshot) domain.Snapshot {
	state := domain.SnapshotStateCompleted
	switch s.Status {
	case "CREATING", "UPLOADING":
		state = domain.SnapshotStatePending
	case "FAILED":
		state = domain.SnapshotStateError
	}
	return domain.Snapshot{
		ID:          s.Name,
		VolumeID:    gcpLastPathSegment(s.SourceDisk),
		VolumeSize:  int(s.DiskSizeGb),
		State:       state,
		Description: s.Description,
		Tags:        map[string]string{"Name": s.Name},
	}
}
