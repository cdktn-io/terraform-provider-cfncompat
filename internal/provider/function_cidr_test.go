// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	tfversion "github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestCidrFunction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		ipBlock     types.String
		count       types.Int64
		cidrBits    types.Int64
		expected    []string
		expectError string
	}{
		// Official AWS doc example: "Fn::Cidr" : [ "192.168.0.0/24", "6", "5"]
		// creates 6 CIDRs with a subnet mask "/27" from a CIDR with mask "/24".
		"doc example: basic usage": {
			ipBlock:  types.StringValue("192.168.0.0/24"),
			count:    types.Int64Value(6),
			cidrBits: types.Int64Value(5),
			expected: []string{
				"192.168.0.0/27",
				"192.168.0.32/27",
				"192.168.0.64/27",
				"192.168.0.96/27",
				"192.168.0.128/27",
				"192.168.0.160/27",
			},
		},
		// Derived from the AWS doc's "Creating an IPv6 enabled VPC" example,
		// which calls Fn::Cidr on the VPC's IPv4 CidrBlock with count=1,
		// cidrBits=8 (32 - 8 = 24). The doc resolves ipBlock via Fn::GetAtt at
		// runtime; a literal /16 stands in for that resolved value here.
		"doc example: ipv6-enabled-vpc, ipv4 subnet with cidr_bits 8": {
			ipBlock:  types.StringValue("10.0.0.0/16"),
			count:    types.Int64Value(1),
			cidrBits: types.Int64Value(8),
			expected: []string{"10.0.0.0/24"},
		},
		// Derived from the same doc example's IPv6 branch: Fn::Cidr on the
		// VPC's (Amazon-provided, /56) Ipv6CidrBlocks entry with count=1,
		// cidrBits=64 (128 - 64 = 64). A literal /56 stands in for the
		// GetAtt/Select-resolved runtime value.
		"doc example: ipv6-enabled-vpc, ipv6 subnet with cidr_bits 64": {
			ipBlock:  types.StringValue("2001:db8:1234:1a00::/56"),
			count:    types.Int64Value(1),
			cidrBits: types.Int64Value(64),
			expected: []string{"2001:db8:1234:1a00::/64"},
		},
		"single subnet spanning the entire ip_block": {
			ipBlock:  types.StringValue("10.0.0.0/16"),
			count:    types.Int64Value(1),
			cidrBits: types.Int64Value(16),
			expected: []string{"10.0.0.0/16"},
		},
		"cidr_bits of 0 produces a single-address subnet": {
			ipBlock:  types.StringValue("10.0.0.0/16"),
			count:    types.Int64Value(1),
			cidrBits: types.Int64Value(0),
			expected: []string{"10.0.0.0/32"},
		},
		"max count of 256": {
			ipBlock:  types.StringValue("10.0.0.0/16"),
			count:    types.Int64Value(256),
			cidrBits: types.Int64Value(8),
			expected: func() []string {
				out := make([]string, 0, 256)
				for i := 0; i < 256; i++ {
					out = append(out, fmt.Sprintf("10.0.%d.0/24", i))
				}
				return out
			}(),
		},
		"ipv4 host bits set is an error": {
			ipBlock:     types.StringValue("192.168.0.5/24"),
			count:       types.Int64Value(1),
			cidrBits:    types.Int64Value(5),
			expectError: `cidr: ip_block "192.168.0.5/24" must be a network address with no host bits set (did you mean "192.168.0.0/24"?)`,
		},
		"invalid ip_block is an error": {
			ipBlock:     types.StringValue("not-a-cidr"),
			count:       types.Int64Value(1),
			cidrBits:    types.Int64Value(5),
			expectError: `cidr: ip_block "not-a-cidr" is not a valid CIDR address block: netip.ParsePrefix("not-a-cidr"): no '/'`,
		},
		"count below 1 is an error": {
			ipBlock:     types.StringValue("192.168.0.0/24"),
			count:       types.Int64Value(0),
			cidrBits:    types.Int64Value(5),
			expectError: "cidr: count must be between 1 and 256, got 0",
		},
		"count above 256 is an error": {
			ipBlock:     types.StringValue("192.168.0.0/16"),
			count:       types.Int64Value(257),
			cidrBits:    types.Int64Value(8),
			expectError: "cidr: count must be between 1 and 256, got 257",
		},
		"negative cidr_bits is an error": {
			ipBlock:     types.StringValue("192.168.0.0/24"),
			count:       types.Int64Value(1),
			cidrBits:    types.Int64Value(-1),
			expectError: "cidr: cidr_bits must be between 0 and 32 for this ip_block, got -1",
		},
		"cidr_bits exceeding address bit length is an error (ipv4)": {
			ipBlock:     types.StringValue("192.168.0.0/24"),
			count:       types.Int64Value(1),
			cidrBits:    types.Int64Value(33),
			expectError: "cidr: cidr_bits must be between 0 and 32 for this ip_block, got 33",
		},
		"cidr_bits exceeding address bit length is an error (ipv6)": {
			ipBlock:     types.StringValue("2001:db8::/32"),
			count:       types.Int64Value(1),
			cidrBits:    types.Int64Value(129),
			expectError: "cidr: cidr_bits must be between 0 and 128 for this ip_block, got 129",
		},
		"cidr_bits produces a block larger than ip_block is an error": {
			ipBlock:     types.StringValue("192.168.0.0/24"),
			count:       types.Int64Value(1),
			cidrBits:    types.Int64Value(9),
			expectError: `cidr: cidr_bits 9 produces a /23 block, which is larger than the /24 ip_block "192.168.0.0/24"`,
		},
		"not enough room for requested count": {
			ipBlock:     types.StringValue("192.168.0.0/24"),
			count:       types.Int64Value(10),
			cidrBits:    types.Int64Value(5),
			expectError: `cidr: ip_block "192.168.0.0/24" cannot fit 10 subnets of /27 (only 8 available)`,
		},
		"null ip_block is an error": {
			ipBlock:     types.StringNull(),
			count:       types.Int64Value(1),
			cidrBits:    types.Int64Value(5),
			expectError: "cidr: ip_block must not be null",
		},
		"null count is an error": {
			ipBlock:     types.StringValue("192.168.0.0/24"),
			count:       types.Int64Null(),
			cidrBits:    types.Int64Value(5),
			expectError: "cidr: count must not be null",
		},
		"null cidr_bits is an error": {
			ipBlock:     types.StringValue("192.168.0.0/24"),
			count:       types.Int64Value(1),
			cidrBits:    types.Int64Null(),
			expectError: "cidr: cidr_bits must not be null",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{tc.ipBlock, tc.count, tc.cidrBits}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.ListUnknown(types.StringType)),
			}

			NewCidrFunction().Run(context.Background(), req, &resp)

			if tc.expectError != "" {
				if resp.Error == nil {
					t.Fatalf("expected error %q, got nil", tc.expectError)
				}
				if resp.Error.Text != tc.expectError {
					t.Fatalf("expected error %q, got %q", tc.expectError, resp.Error.Text)
				}
				return
			}

			if resp.Error != nil {
				t.Fatalf("unexpected error: %s", resp.Error.Text)
			}

			got, ok := resp.Result.Value().(types.List)
			if !ok {
				t.Fatalf("expected result of type types.List, got %T", resp.Result.Value())
			}

			elements := got.Elements()
			if len(elements) != len(tc.expected) {
				t.Fatalf("expected %d elements, got %d", len(tc.expected), len(elements))
			}

			for i, elem := range elements {
				strVal, ok := elem.(types.String)
				if !ok {
					t.Fatalf("element %d: expected types.String, got %T", i, elem)
				}
				if strVal.ValueString() != tc.expected[i] {
					t.Fatalf("element %d: expected %q, got %q", i, tc.expected[i], strVal.ValueString())
				}
			}
		})
	}
}

// TestAccCidrFunction is an acceptance test exercising provider::cfncompat::cidr
// through a real Terraform CLI run. It is gated by TF_ACC=1 and is not run as
// part of `make test`; it is registered here for the batch acceptance run
// after provider.go wiring.
func TestAccCidrFunction(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::cidr("192.168.0.0/24", 6, 5)
				}`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownOutputValue("test", knownvalue.ListExact([]knownvalue.Check{
						knownvalue.StringExact("192.168.0.0/27"),
						knownvalue.StringExact("192.168.0.32/27"),
						knownvalue.StringExact("192.168.0.64/27"),
						knownvalue.StringExact("192.168.0.96/27"),
						knownvalue.StringExact("192.168.0.128/27"),
						knownvalue.StringExact("192.168.0.160/27"),
					})),
				},
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::cidr("2001:db8:1234:1a00::/56", 1, 64)
				}`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownOutputValue("test", knownvalue.ListExact([]knownvalue.Check{
						knownvalue.StringExact("2001:db8:1234:1a00::/64"),
					})),
				},
			},
		},
	})
}
