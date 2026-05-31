// Live conformance: start a real `coredns` server pointed at the
// backend's directory, write records via the backend, query via
// miekg/dns, and verify resolution. Validates the file format the
// backend emits is what CoreDNS expects — the canonical end-to-end
// check for the K8s peer's "fourth backend" slot.
//
// Skipped if the `coredns` binary isn't on PATH (CI provisions it
// via the workflow's CoreDNS install step; local devs install
// manually).
package coredns

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/e6qu/shimanism/internal/dns/domain"
)

func requireCoreDNS(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("coredns")
	if err != nil {
		t.Skipf("coredns binary not on PATH (CI installs it via the sockerless e2e workflow); skipping live conformance: %v", err)
	}
	return bin
}

// freeUDPPort grabs an ephemeral UDP port the test can hand to
// coredns. CoreDNS binds the same port for both UDP and TCP; we
// only probe UDP here since the resolver test below uses it.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve UDP addr: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	_ = conn.Close()
	return port
}

func TestLive_CoreDNS_ResolvesRecordsWrittenByBackend(t *testing.T) {
	bin := requireCoreDNS(t)
	ctx := context.Background()

	zoneDir := t.TempDir()
	b, err := New(zoneDir)
	if err != nil {
		t.Fatalf("New backend: %v", err)
	}
	// Seed a zone + records BEFORE starting CoreDNS so the auto
	// plugin picks them up on first scan.
	if _, err := b.CreateZone(ctx, "live.example.", domain.CreateZoneOptions{}); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	if err := b.PutRecordSet(ctx, "live.example.", domain.RecordSet{
		Name: "api.live.example.", Type: domain.RecordTypeA, TTL: 60,
		Records: []string{"203.0.113.7"},
	}); err != nil {
		t.Fatalf("PutRecordSet A: %v", err)
	}
	if err := b.PutRecordSet(ctx, "live.example.", domain.RecordSet{
		Name: "v6.live.example.", Type: domain.RecordTypeAAAA, TTL: 60,
		Records: []string{"2001:db8::1"},
	}); err != nil {
		t.Fatalf("PutRecordSet AAAA: %v", err)
	}

	port := freeUDPPort(t)
	corefile := filepath.Join(t.TempDir(), "Corefile")
	corefileContent := fmt.Sprintf(`. {
    auto {
        directory %s (.*)\.db {1}
        reload 1s
    }
    bind 127.0.0.1
}
`, zoneDir)
	if err := os.WriteFile(corefile, []byte(corefileContent), 0o644); err != nil {
		t.Fatalf("write Corefile: %v", err)
	}

	cmd := exec.CommandContext(ctx, bin,
		"-conf", corefile,
		"-dns.port", strconv.Itoa(port),
	)
	cmd.Stdout = nil // suppress; flip on for debugging
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("start coredns: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Wait for CoreDNS to bind + the auto-plugin to load the zone.
	deadline := time.Now().Add(5 * time.Second)
	client := &dns.Client{Net: "udp", Timeout: 500 * time.Millisecond}
	server := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		m := new(dns.Msg)
		m.SetQuestion("api.live.example.", dns.TypeA)
		resp, _, err := client.Exchange(m, server)
		if err == nil && resp != nil && len(resp.Answer) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// A record.
	m := new(dns.Msg)
	m.SetQuestion("api.live.example.", dns.TypeA)
	resp, _, err := client.Exchange(m, server)
	if err != nil {
		t.Fatalf("A query: %v", err)
	}
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("A rcode = %s, want NOERROR", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("A answer len = %d, want 1", len(resp.Answer))
	}
	aRR, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("A answer wrong type: %T", resp.Answer[0])
	}
	if aRR.A.String() != "203.0.113.7" {
		t.Errorf("A.A = %s, want 203.0.113.7", aRR.A.String())
	}

	// AAAA record.
	m = new(dns.Msg)
	m.SetQuestion("v6.live.example.", dns.TypeAAAA)
	resp, _, err = client.Exchange(m, server)
	if err != nil {
		t.Fatalf("AAAA query: %v", err)
	}
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("AAAA rcode = %s, want NOERROR", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("AAAA answer len = %d, want 1", len(resp.Answer))
	}
	aaaaRR, ok := resp.Answer[0].(*dns.AAAA)
	if !ok {
		t.Fatalf("AAAA answer wrong type: %T", resp.Answer[0])
	}
	if aaaaRR.AAAA.String() != "2001:db8::1" {
		t.Errorf("AAAA.AAAA = %s, want 2001:db8::1", aaaaRR.AAAA.String())
	}

	// SOA — confirms the apex bootstrap is what CoreDNS sees too.
	m = new(dns.Msg)
	m.SetQuestion("live.example.", dns.TypeSOA)
	resp, _, err = client.Exchange(m, server)
	if err != nil {
		t.Fatalf("SOA query: %v", err)
	}
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("SOA rcode = %s, want NOERROR", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("SOA answer len = %d, want 1", len(resp.Answer))
	}
	if _, ok := resp.Answer[0].(*dns.SOA); !ok {
		t.Errorf("SOA answer wrong type: %T", resp.Answer[0])
	}
}

// TestLive_CoreDNS_PicksUpRuntimeChanges checks that records written
// AFTER CoreDNS is running (the auto-plugin reload path) become
// resolvable. CoreDNS's auto plugin polls every `reload` interval.
func TestLive_CoreDNS_PicksUpRuntimeChanges(t *testing.T) {
	bin := requireCoreDNS(t)
	ctx := context.Background()

	zoneDir := t.TempDir()
	b, err := New(zoneDir)
	if err != nil {
		t.Fatalf("New backend: %v", err)
	}
	// Seed the zone before start so CoreDNS has SOMETHING to load.
	if _, err := b.CreateZone(ctx, "reload.example.", domain.CreateZoneOptions{}); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}

	port := freeUDPPort(t)
	corefile := filepath.Join(t.TempDir(), "Corefile")
	if err := os.WriteFile(corefile, []byte(fmt.Sprintf(
		`. {
    auto {
        directory %s (.*)\.db {1}
        reload 1s
    }
    bind 127.0.0.1
}
`, zoneDir)), 0o644); err != nil {
		t.Fatalf("write Corefile: %v", err)
	}
	cmd := exec.CommandContext(ctx, bin, "-conf", corefile, "-dns.port", strconv.Itoa(port))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start coredns: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	// Wait for first load.
	time.Sleep(2 * time.Second)

	// Add a record after CoreDNS is running. We rewrite the zone
	// file with a new serial (the backend's writeZoneFile already
	// rewrites the file atomically; CoreDNS's auto plugin detects
	// the mtime change).
	if err := b.PutRecordSet(ctx, "reload.example.", domain.RecordSet{
		Name: "new.reload.example.", Type: domain.RecordTypeA, TTL: 60,
		Records: []string{"198.51.100.4"},
	}); err != nil {
		t.Fatalf("PutRecordSet: %v", err)
	}
	// CoreDNS auto's reload poll interval is 1s; give it some slack.
	time.Sleep(3 * time.Second)

	client := &dns.Client{Net: "udp", Timeout: 500 * time.Millisecond}
	server := fmt.Sprintf("127.0.0.1:%d", port)
	m := new(dns.Msg)
	m.SetQuestion("new.reload.example.", dns.TypeA)
	resp, _, err := client.Exchange(m, server)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %s, want NOERROR (auto-plugin didn't pick up the new record?)",
			dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("answer len = %d, want 1", len(resp.Answer))
	}
}
