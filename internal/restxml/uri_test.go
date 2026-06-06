package restxml

import (
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestMatchPathPreservesEscapedSlashInLabel(t *testing.T) {
	arn := "arn:aws:kafka:us-east-1:000000000000:cluster/cluster-a/uuid-1"
	req := httptest.NewRequest("GET", "http://shim.test/v1/clusters/"+url.PathEscape(arn)+"/topics/orders", nil)

	labels, ok := MatchURI(MatchPath(req), "/v1/clusters/{ClusterArn}/topics/{TopicName}")
	if !ok {
		t.Fatal("MatchURI: got no match")
	}
	if labels["ClusterArn"] != arn {
		t.Fatalf("ClusterArn = %q, want %q", labels["ClusterArn"], arn)
	}
	if labels["TopicName"] != "orders" {
		t.Fatalf("TopicName = %q, want orders", labels["TopicName"])
	}
}
