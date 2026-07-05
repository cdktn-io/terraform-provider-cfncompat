// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"math/big"
	"net/netip"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure CidrFunction satisfies the function.Function interface.
var _ function.Function = CidrFunction{}

// NewCidrFunction returns a new instance of the provider::cfncompat::cidr
// function.
func NewCidrFunction() function.Function {
	return CidrFunction{}
}

// CidrFunction implements CloudFormation's Fn::Cidr intrinsic function: it
// splits an IP address block into a number of smaller, consecutive CIDR
// blocks.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-cidr.html
type CidrFunction struct{}

func (f CidrFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "cidr"
}

func (f CidrFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Split a CIDR block into smaller, consecutive CIDR blocks",
		Description: "Returns an array of count CIDR address blocks, split from ip_block. cidr_bits is the number " +
			"of subnet bits: the resulting prefix length is 32 - cidr_bits for IPv4 blocks, or 128 - cidr_bits for " +
			"IPv6 blocks. count must be between 1 and 256. Subnets are allocated consecutively starting from the " +
			"beginning of ip_block.",
		MarkdownDescription: "Returns an array of `count` CIDR address blocks, split from `ip_block`.\n\n" +
			"Matches the semantics of CloudFormation's " +
			"[`Fn::Cidr`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-cidr.html) " +
			"intrinsic function.\n\n" +
			"`cidr_bits` is the number of subnet bits for the generated CIDRs: the resulting prefix length is " +
			"`32 - cidr_bits` for an IPv4 `ip_block`, or `128 - cidr_bits` for an IPv6 `ip_block`. For example, " +
			"`cidr_bits = 8` against an IPv4 block produces `/24` subnets. `count` must be between 1 and 256. " +
			"Subnets are allocated consecutively starting from the beginning of `ip_block`, and `ip_block` must be " +
			"large enough to contain `count` subnets of the requested size. Both IPv4 and IPv6 blocks are supported.",
		Parameters: []function.Parameter{
			function.StringParameter{
				Name:                "ip_block",
				Description:         "The user-specified CIDR address block (IPv4 or IPv6) to be split into smaller CIDR blocks.",
				MarkdownDescription: "The user-specified CIDR address block (IPv4 or IPv6) to be split into smaller CIDR blocks.",
			},
			function.Int64Parameter{
				Name:                "count",
				Description:         "The number of CIDRs to generate. Valid range is between 1 and 256.",
				MarkdownDescription: "The number of CIDRs to generate. Valid range is between 1 and 256.",
			},
			function.Int64Parameter{
				Name:                "cidr_bits",
				Description:         "The number of subnet bits for the CIDR. The resulting prefix length is 32 - cidr_bits (IPv4) or 128 - cidr_bits (IPv6).",
				MarkdownDescription: "The number of subnet bits for the CIDR. The resulting prefix length is `32 - cidr_bits` (IPv4) or `128 - cidr_bits` (IPv6).",
			},
		},
		Return: function.ListReturn{ElementType: types.StringType},
	}
}

func (f CidrFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var ipBlock types.String
	var count types.Int64
	var cidrBits types.Int64

	resp.Error = req.Arguments.Get(ctx, &ipBlock, &count, &cidrBits)
	if resp.Error != nil {
		return
	}

	if ipBlock.IsUnknown() || count.IsUnknown() || cidrBits.IsUnknown() {
		return
	}

	if ipBlock.IsNull() {
		resp.Error = function.NewArgumentFuncError(0, "cidr: ip_block must not be null")
		return
	}

	if count.IsNull() {
		resp.Error = function.NewArgumentFuncError(1, "cidr: count must not be null")
		return
	}

	if cidrBits.IsNull() {
		resp.Error = function.NewArgumentFuncError(2, "cidr: cidr_bits must not be null")
		return
	}

	countVal := count.ValueInt64()
	if countVal < 1 || countVal > 256 {
		resp.Error = function.NewArgumentFuncError(1, fmt.Sprintf(
			"cidr: count must be between 1 and 256, got %d", countVal,
		))
		return
	}

	prefix, err := netip.ParsePrefix(ipBlock.ValueString())
	if err != nil {
		resp.Error = function.NewArgumentFuncError(0, fmt.Sprintf(
			"cidr: ip_block %q is not a valid CIDR address block: %s", ipBlock.ValueString(), err,
		))
		return
	}

	if prefix != prefix.Masked() {
		resp.Error = function.NewArgumentFuncError(0, fmt.Sprintf(
			"cidr: ip_block %q must be a network address with no host bits set (did you mean %q?)",
			ipBlock.ValueString(), prefix.Masked().String(),
		))
		return
	}

	addrBitLen := 32
	if !prefix.Addr().Is4() {
		addrBitLen = 128
	}

	cidrBitsVal := cidrBits.ValueInt64()
	if cidrBitsVal < 0 || cidrBitsVal > int64(addrBitLen) {
		resp.Error = function.NewArgumentFuncError(2, fmt.Sprintf(
			"cidr: cidr_bits must be between 0 and %d for this ip_block, got %d", addrBitLen, cidrBitsVal,
		))
		return
	}

	newPrefixLen := addrBitLen - int(cidrBitsVal)
	if newPrefixLen < prefix.Bits() {
		resp.Error = function.NewArgumentFuncError(2, fmt.Sprintf(
			"cidr: cidr_bits %d produces a /%d block, which is larger than the /%d ip_block %q",
			cidrBitsVal, newPrefixLen, prefix.Bits(), ipBlock.ValueString(),
		))
		return
	}

	availableBits := newPrefixLen - prefix.Bits()

	maxSubnets := new(big.Int).Lsh(big.NewInt(1), uint(availableBits))
	if maxSubnets.Cmp(big.NewInt(countVal)) < 0 {
		resp.Error = function.NewFuncError(fmt.Sprintf(
			"cidr: ip_block %q cannot fit %d subnets of /%d (only %s available)",
			ipBlock.ValueString(), countVal, newPrefixLen, maxSubnets.String(),
		))
		return
	}

	subnets, err := cidrGenerateSubnets(prefix, newPrefixLen, addrBitLen, countVal)
	if err != nil {
		resp.Error = function.NewFuncError(fmt.Sprintf("cidr: %s", err))
		return
	}

	resp.Error = resp.Result.Set(ctx, subnets)
}

// cidrGenerateSubnets returns countVal consecutive CIDR blocks of prefix
// length newPrefixLen, allocated starting from the beginning of base.
// addrBitLen is 32 for IPv4 blocks and 128 for IPv6 blocks.
func cidrGenerateSubnets(base netip.Prefix, newPrefixLen, addrBitLen int, countVal int64) ([]string, error) {
	addrBytes := base.Addr().As16()
	byteLen := addrBitLen / 8

	baseInt := new(big.Int).SetBytes(addrBytes[16-byteLen:])
	subnetSize := new(big.Int).Lsh(big.NewInt(1), uint(addrBitLen-newPrefixLen))

	subnets := make([]string, 0, countVal)

	for i := int64(0); i < countVal; i++ {
		offset := new(big.Int).Mul(big.NewInt(i), subnetSize)
		subnetInt := new(big.Int).Add(baseInt, offset)

		subnetBytes := make([]byte, byteLen)
		subnetInt.FillBytes(subnetBytes)

		var addr netip.Addr
		if byteLen == 4 {
			var b4 [4]byte
			copy(b4[:], subnetBytes)
			addr = netip.AddrFrom4(b4)
		} else {
			var b16 [16]byte
			copy(b16[:], subnetBytes)
			addr = netip.AddrFrom16(b16)
		}

		subnetPrefix, err := addr.Prefix(newPrefixLen)
		if err != nil {
			return nil, fmt.Errorf("failed to build subnet %d: %w", i, err)
		}

		subnets = append(subnets, subnetPrefix.String())
	}

	return subnets, nil
}
