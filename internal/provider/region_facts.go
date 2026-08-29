// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import "strings"

// defaultPartition is the AWS partition used for every region that does not
// match one of the regionFacts prefixes (i.e. the commercial partition).
const defaultPartition = "aws"

// defaultURLSuffix is the DNS suffix of the commercial partition, and the
// fallback for any partition name this provider does not know.
const defaultURLSuffix = "amazonaws.com"

// regionFact maps a region-name prefix onto the AWS partition that region
// belongs to and the partition's DNS suffix (CloudFormation's
// AWS::URLSuffix).
type regionFact struct {
	// prefix is matched against the start of the region name. The empty
	// prefix is not used: the commercial partition is the fallthrough
	// default (defaultPartition/defaultURLSuffix).
	prefix string
	// partition is CloudFormation's AWS::Partition for regions with prefix.
	partition string
	// urlSuffix is CloudFormation's AWS::URLSuffix for partition.
	urlSuffix string
}

// regionFacts is the partition / URL-suffix table, mirrored from aws-cdk's
// PARTITION_MAP (region-info/lib/aws-entities.ts), the table behind
// RegionInfo.get(region).partition / .domainSuffix.
//
// Why this provider carries its own copy instead of reusing aws-cdk's
// region-info package: the table is consulted at *apply* time, inside a Go
// plugin process, where no TypeScript package is reachable -- the consumers
// that do have region-info (aws-cdk-lib, TerraConstructs, the CDK Terrain
// synthesis backend) use it at synth time to emit literals whenever the
// region is literal (RFC 006 s5), and only reach for this data source when
// the region is not known until apply. aws-sdk-go-v2 exports no public
// partition metadata either (v1's endpoints.PartitionForRegion has no v2
// equivalent), which is why hashicorp/aws ships the same kind of static
// table (names/partition.go). It is the only source for AWS::URLSuffix -- no
// AWS API returns the DNS suffix. Eight rows that change once every few
// years; keep in sync with aws-entities.ts when AWS opens a partition.
//
// The pseudo-parameters data source derives AWS::Partition from the
// configured region through this table (CloudFormation's own rule: the
// partition of the region the stack is deployed in); the STS caller ARN's
// partition is used only to warn on a mismatch.
//
// Entries are ordered longest-prefix-first so that lookup is unambiguous
// even if a future prefix becomes a prefix of another one (today's prefixes
// all end in "-" and cannot overlap: "us-isob-east-1" does not start with
// "us-iso-").
var regionFacts = []regionFact{
	{prefix: "eusc-de-", partition: "aws-eusc", urlSuffix: "amazonaws.eu"},
	{prefix: "eu-isoe-", partition: "aws-iso-e", urlSuffix: "cloud.adc-e.uk"},
	{prefix: "us-isob-", partition: "aws-iso-b", urlSuffix: "sc2s.sgov.gov"},
	{prefix: "us-isof-", partition: "aws-iso-f", urlSuffix: "csp.hci.ic.gov"},
	{prefix: "us-gov-", partition: "aws-us-gov", urlSuffix: defaultURLSuffix},
	{prefix: "us-iso-", partition: "aws-iso", urlSuffix: "c2s.ic.gov"},
	{prefix: "cn-", partition: "aws-cn", urlSuffix: "amazonaws.com.cn"},
}

// partitionForRegion returns the AWS partition (CloudFormation's
// AWS::Partition) a region belongs to, defaulting to the commercial
// partition "aws" for any region -- including an empty or unknown one --
// that matches no known prefix.
func partitionForRegion(region string) string {
	for _, f := range regionFacts {
		if strings.HasPrefix(region, f.prefix) {
			return f.partition
		}
	}
	return defaultPartition
}

// urlSuffixForPartition returns the DNS suffix (CloudFormation's
// AWS::URLSuffix) of an AWS partition, defaulting to "amazonaws.com" for the
// commercial partition and for any partition name not in the table.
//
// Note that "aws" and "aws-us-gov" share the "amazonaws.com" suffix, so the
// mapping partition -> suffix is well defined even though it is not
// injective.
func urlSuffixForPartition(partition string) string {
	for _, f := range regionFacts {
		if f.partition == partition {
			return f.urlSuffix
		}
	}
	return defaultURLSuffix
}
