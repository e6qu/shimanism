// Tests for the ec2Query emitter path. Asserts the emitter produces
// gofmt-clean Go that: (1) imports internal/ec2query (not
// internal/awsquery), (2) uses flattened list serialisation (Field.N
// with no .member. interfix), and (3) emits the correct registration
// and backend interface symbols.
package codegen_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/codegen/emit"
	"github.com/e6qu/shimanism/internal/codegen/smithy"
)

// minimalEC2QueryModel is a synthetic Smithy model that declares the
// ec2Query protocol — used to test the emitter without requiring the
// real EC2 spec to be vendored.
const minimalEC2QueryModel = `{
  "smithy": "2.0",
  "shapes": {
    "com.amazonaws.ec2#EC2": {
      "type": "service",
      "version": "2016-11-15",
      "operations": [
        {"target": "com.amazonaws.ec2#DescribeVpcs"},
        {"target": "com.amazonaws.ec2#CreateVpc"}
      ],
      "traits": {
        "aws.api#service": {"sdkId": "EC2", "endpointPrefix": "ec2"},
        "aws.auth#sigv4": {"name": "ec2"},
        "aws.protocols#ec2Query": {}
      }
    },
    "com.amazonaws.ec2#DescribeVpcs": {
      "type": "operation",
      "input": {"target": "com.amazonaws.ec2#DescribeVpcsRequest"},
      "output": {"target": "com.amazonaws.ec2#DescribeVpcsResult"}
    },
    "com.amazonaws.ec2#DescribeVpcsRequest": {
      "type": "structure",
      "members": {
        "VpcIds": {"target": "com.amazonaws.ec2#VpcIdStringList"},
        "MaxResults": {"target": "smithy.api#Integer"}
      }
    },
    "com.amazonaws.ec2#DescribeVpcsResult": {
      "type": "structure",
      "members": {
        "Vpcs": {"target": "com.amazonaws.ec2#VpcList"},
        "NextToken": {"target": "smithy.api#String"}
      }
    },
    "com.amazonaws.ec2#CreateVpc": {
      "type": "operation",
      "input": {"target": "com.amazonaws.ec2#CreateVpcRequest"},
      "output": {"target": "com.amazonaws.ec2#CreateVpcResult"}
    },
    "com.amazonaws.ec2#CreateVpcRequest": {
      "type": "structure",
      "members": {
        "CidrBlock": {"target": "smithy.api#String"},
        "AmazonProvidedIpv6CidrBlock": {"target": "smithy.api#Boolean"}
      }
    },
    "com.amazonaws.ec2#CreateVpcResult": {
      "type": "structure",
      "members": {
        "Vpc": {"target": "com.amazonaws.ec2#Vpc"}
      }
    },
    "com.amazonaws.ec2#Vpc": {
      "type": "structure",
      "members": {
        "VpcId": {"target": "smithy.api#String"},
        "CidrBlock": {"target": "smithy.api#String"},
        "State": {"target": "smithy.api#String"}
      }
    },
    "com.amazonaws.ec2#VpcList": {
      "type": "list",
      "member": {"target": "com.amazonaws.ec2#Vpc"}
    },
    "com.amazonaws.ec2#VpcIdStringList": {
      "type": "list",
      "member": {"target": "smithy.api#String"}
    }
  }
}`

func TestCodegen_EC2Query_EmitsValidGo(t *testing.T) {
	model, err := smithy.Parse([]byte(minimalEC2QueryModel))
	if err != nil {
		t.Fatalf("parse synthetic model: %v", err)
	}

	got, err := emit.Emit(model, emit.Options{
		PackageName:  "gen",
		SourceFile:   "services/compute/spec/aws-ec2.smithy.json",
		SourceCommit: "0000000000000000000000000000000000000000",
		Operations:   []string{"DescribeVpcs", "CreateVpc"},
	})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "aws_ec2.gen.go", got, parser.AllErrors); err != nil {
		t.Fatalf("emitted Go is not parseable: %v\n--- source ---\n%s", err, got)
	}

	src := string(got)

	// Must import internal/ec2query, not internal/awsquery.
	if !strings.Contains(src, `"github.com/e6qu/shimanism/internal/ec2query"`) {
		t.Error("emitted file does not import internal/ec2query")
	}
	if strings.Contains(src, `"github.com/e6qu/shimanism/internal/awsquery"`) {
		t.Error("emitted file must not import internal/awsquery for ec2Query protocol")
	}

	// Registration function uses ec2query.Router.
	if !strings.Contains(src, "RegisterEC2Routes") {
		t.Error("missing RegisterEC2Routes symbol")
	}
	if !strings.Contains(src, "*ec2query.Router") {
		t.Error("missing *ec2query.Router return type on registration function")
	}

	// Backend interfaces emitted for both operations.
	for _, sym := range []string{"DescribeVpcsBackend", "CreateVpcBackend", "EC2Backend"} {
		if !strings.Contains(src, sym) {
			t.Errorf("missing symbol %q in emitted source", sym)
		}
	}

	// Flattened list decoding: `VpcIds.N` with NO `.member.` interfix.
	if !strings.Contains(src, `"VpcIds."`) {
		t.Error("ec2Query list decode must use flattened Field.N (\"VpcIds.\" + index)")
	}
	if strings.Contains(src, `"VpcIds.member."`) {
		t.Error("ec2Query must NOT use .member. interfix in list decoding (that is awsQuery)")
	}

	// Response must use ec2query.WriteResult and ec2query.WriteBackendError.
	if !strings.Contains(src, "ec2query.WriteResult") {
		t.Error("missing ec2query.WriteResult call in emitted handlers")
	}
	if !strings.Contains(src, "ec2query.WriteBackendError") {
		t.Error("missing ec2query.WriteBackendError call in emitted handlers")
	}
}
