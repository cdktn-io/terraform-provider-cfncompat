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
// No AWS API returns the DNS suffix, so a static table is the only source
// for AWS::URLSuffix -- hashicorp/aws's aws_partition.dns_suffix is likewise
// a static SDK table. The partition, in contrast, is only the fallback here:
// the pseudo-parameters data source prefers the partition of the STS caller
// ARN, which is authoritative.
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
