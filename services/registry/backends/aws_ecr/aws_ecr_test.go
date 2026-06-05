package aws_ecr

import (
	"encoding/base64"
	"errors"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"

	"github.com/e6qu/shimanism/internal/registry/domain"
)

func TestDecodeAuthorizationToken(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte("AWS:secret"))
	user, pass, err := decodeAuthorizationToken(token)
	if err != nil {
		t.Fatalf("decodeAuthorizationToken: %v", err)
	}
	if user != "AWS" || pass != "secret" {
		t.Fatalf("decodeAuthorizationToken = %q/%q, want AWS/secret", user, pass)
	}
}

func TestDecodeAuthorizationTokenRejectsMalformedToken(t *testing.T) {
	for _, token := range []string{"", "not-base64", base64.StdEncoding.EncodeToString([]byte("missing-colon"))} {
		if _, _, err := decodeAuthorizationToken(token); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("decodeAuthorizationToken(%q): want ErrInvalidInput, got %v", token, err)
		}
	}
}

func TestSelectAuthMatchesRegistryID(t *testing.T) {
	data := []ecrtypes.AuthorizationData{
		{
			AuthorizationToken: awsapi.String("token-a"),
			ProxyEndpoint:      awsapi.String("https://111111111111.dkr.ecr.us-east-1.amazonaws.com"),
		},
		{
			AuthorizationToken: awsapi.String("token-b"),
			ProxyEndpoint:      awsapi.String("https://222222222222.dkr.ecr.us-east-1.amazonaws.com"),
		},
	}
	auth, err := selectAuth(data, "222222222222")
	if err != nil {
		t.Fatalf("selectAuth: %v", err)
	}
	if got := awsapi.ToString(auth.AuthorizationToken); got != "token-b" {
		t.Fatalf("selectAuth token = %q, want token-b", got)
	}
}
