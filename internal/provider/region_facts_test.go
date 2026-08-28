// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"strings"
	"testing"
)

// TestPartitionAndURLSuffixForRegion pins the partition / URL-suffix table
// against aws-cdk's PARTITION_MAP (region-info/lib/aws-entities.ts), the
// table CloudFormation's AWS::Partition and AWS::URLSuffix are resolved
// from, with one real region per partition plus the fallthrough cases.
func TestPartitionAndURLSuffixForRegion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		region        string
		wantPartition string
		wantURLSuffix string
	}{
		{name: "commercial us-east-1", region: "us-east-1", wantPartition: "aws", wantURLSuffix: "amazonaws.com"},
		{name: "commercial eu-west-1", region: "eu-west-1", wantPartition: "aws", wantURLSuffix: "amazonaws.com"},
		{name: "commercial ap-southeast-2", region: "ap-southeast-2", wantPartition: "aws", wantURLSuffix: "amazonaws.com"},
		{name: "china cn-north-1", region: "cn-north-1", wantPartition: "aws-cn", wantURLSuffix: "amazonaws.com.cn"},
		{name: "china cn-northwest-1", region: "cn-northwest-1", wantPartition: "aws-cn", wantURLSuffix: "amazonaws.com.cn"},
		{name: "govcloud us-gov-west-1", region: "us-gov-west-1", wantPartition: "aws-us-gov", wantURLSuffix: "amazonaws.com"},
		{name: "iso us-iso-east-1", region: "us-iso-east-1", wantPartition: "aws-iso", wantURLSuffix: "c2s.ic.gov"},
		{name: "iso-b us-isob-east-1", region: "us-isob-east-1", wantPartition: "aws-iso-b", wantURLSuffix: "sc2s.sgov.gov"},
		{name: "iso-f us-isof-south-1", region: "us-isof-south-1", wantPartition: "aws-iso-f", wantURLSuffix: "csp.hci.ic.gov"},
		{name: "iso-e eu-isoe-west-1", region: "eu-isoe-west-1", wantPartition: "aws-iso-e", wantURLSuffix: "cloud.adc-e.uk"},
		{name: "eusc eusc-de-east-1", region: "eusc-de-east-1", wantPartition: "aws-eusc", wantURLSuffix: "amazonaws.eu"},
		// Fallthrough cases: anything unrecognised is the commercial
		// partition, exactly as PARTITION_MAP.default is in aws-cdk.
		{name: "empty region", region: "", wantPartition: "aws", wantURLSuffix: "amazonaws.com"},
		{name: "unknown region", region: "mars-north-1", wantPartition: "aws", wantURLSuffix: "amazonaws.com"},
		// "us-iso" without the trailing dash must NOT match "us-iso-", and
		// "us-isob-*" must not be mistaken for the "us-iso-" partition.
		{name: "us-iso without separator", region: "us-isolated-1", wantPartition: "aws", wantURLSuffix: "amazonaws.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := partitionForRegion(tt.region); got != tt.wantPartition {
				t.Errorf("partitionForRegion(%q) = %q, want %q", tt.region, got, tt.wantPartition)
			}
			if got := urlSuffixForRegion(tt.region); got != tt.wantURLSuffix {
				t.Errorf("urlSuffixForRegion(%q) = %q, want %q", tt.region, got, tt.wantURLSuffix)
			}
			if got := urlSuffixForPartition(tt.wantPartition); got != tt.wantURLSuffix {
				t.Errorf("urlSuffixForPartition(%q) = %q, want %q", tt.wantPartition, got, tt.wantURLSuffix)
			}
		})
	}
}

// TestURLSuffixForPartition covers the partition -> suffix direction on its
// own, including the unknown-partition fallback (a partition name that comes
// from an STS caller ARN this provider's table does not know about).
func TestURLSuffixForPartition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		partition string
		want      string
	}{
		{partition: "aws", want: "amazonaws.com"},
		{partition: "aws-cn", want: "amazonaws.com.cn"},
		{partition: "aws-us-gov", want: "amazonaws.com"},
		{partition: "aws-iso", want: "c2s.ic.gov"},
		{partition: "aws-iso-b", want: "sc2s.sgov.gov"},
		{partition: "aws-iso-f", want: "csp.hci.ic.gov"},
		{partition: "aws-iso-e", want: "cloud.adc-e.uk"},
		{partition: "aws-eusc", want: "amazonaws.eu"},
		// A partition added to AWS after this provider was built still gets
		// the commercial suffix rather than an empty string.
		{partition: "aws-future", want: "amazonaws.com"},
		{partition: "", want: "amazonaws.com"},
	}

	for _, tt := range tests {
		t.Run(tt.partition, func(t *testing.T) {
			t.Parallel()

			if got := urlSuffixForPartition(tt.partition); got != tt.want {
				t.Errorf("urlSuffixForPartition(%q) = %q, want %q", tt.partition, got, tt.want)
			}
		})
	}
}

// TestRegionFactsTableIsConsistent guards the table itself: every entry must
// be fully populated, and a partition must never map to two different URL
// suffixes (urlSuffixForPartition returns the first match, so an
// inconsistent table would silently pick one).
func TestRegionFactsTableIsConsistent(t *testing.T) {
	t.Parallel()

	suffixes := map[string]string{}
	for _, f := range regionFacts {
		if f.prefix == "" || f.partition == "" || f.urlSuffix == "" {
			t.Errorf("incomplete regionFacts entry: %+v", f)
			continue
		}
		if seen, ok := suffixes[f.partition]; ok && seen != f.urlSuffix {
			t.Errorf("partition %q maps to both %q and %q", f.partition, seen, f.urlSuffix)
			continue
		}
		suffixes[f.partition] = f.urlSuffix
	}
}

// TestRegionFactsAreLongestPrefixFirst pins the ordering invariant the table
// documents: partitionForRegion returns the first prefix that matches, so a
// shorter prefix listed before a longer one it is a prefix of would shadow
// it (e.g. "us-iso-" ahead of "us-isob-"). No entry may therefore be a
// prefix of an entry listed after it.
func TestRegionFactsAreLongestPrefixFirst(t *testing.T) {
	t.Parallel()

	for i := range regionFacts {
		for j := i + 1; j < len(regionFacts); j++ {
			if strings.HasPrefix(regionFacts[j].prefix, regionFacts[i].prefix) {
				t.Errorf(
					"regionFacts[%d].prefix %q is a prefix of regionFacts[%d].prefix %q, which it would shadow: list the longer prefix first",
					i, regionFacts[i].prefix, j, regionFacts[j].prefix,
				)
			}
		}
	}
}
