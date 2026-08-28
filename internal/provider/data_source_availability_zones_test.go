// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	rtresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	rtterraform "github.com/hashicorp/terraform-plugin-testing/terraform"
)

// fakeAvailabilityZonesAPI is an availabilityZonesAPI fake. Zones are
// returned verbatim from DescribeAvailabilityZones (so the data source's own
// state/zone-type filtering and sorting are what is under test), and
// defaultSubnetZones is turned into one DescribeSubnets page per entry of
// subnetPages when set, so pagination is exercised too.
type fakeAvailabilityZonesAPI struct {
	zones []ec2types.AvailabilityZone
	// subnetPages is the sequence of DescribeSubnets responses, each a list
	// of zone names that have a default subnet. A single page is the common
	// case; more than one exercises NextToken paging.
	subnetPages [][]string
	// echoSubnetToken makes every DescribeSubnets page hand back the token
	// it was called with (and a fixed token on the first call), i.e. the
	// pathological response that would page forever.
	echoSubnetToken bool

	zonesErr   error
	subnetsErr error

	describeZonesCalls   int
	describeSubnetsCalls int
	// lastSubnetFilters records the filters of the most recent
	// DescribeSubnets call, so the default-for-az filter can be asserted.
	lastSubnetFilters []ec2types.Filter
}

func (f *fakeAvailabilityZonesAPI) DescribeAvailabilityZones(_ context.Context, _ *ec2.DescribeAvailabilityZonesInput, _ ...func(*ec2.Options)) (*ec2.DescribeAvailabilityZonesOutput, error) {
	f.describeZonesCalls++
	if f.zonesErr != nil {
		return nil, f.zonesErr
	}
	return &ec2.DescribeAvailabilityZonesOutput{AvailabilityZones: f.zones}, nil
}

func (f *fakeAvailabilityZonesAPI) DescribeSubnets(_ context.Context, in *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	f.describeSubnetsCalls++
	if f.subnetsErr != nil {
		return nil, f.subnetsErr
	}
	f.lastSubnetFilters = in.Filters

	if f.echoSubnetToken {
		out := &ec2.DescribeSubnetsOutput{NextToken: aws.String("stuck")}
		if in.NextToken != nil {
			out.NextToken = in.NextToken
		}
		return out, nil
	}

	page := 0
	if in.NextToken != nil {
		// The fake's tokens are just the (1-based) index of the next page.
		page = len(*in.NextToken)
	}
	if page >= len(f.subnetPages) {
		return &ec2.DescribeSubnetsOutput{}, nil
	}

	out := &ec2.DescribeSubnetsOutput{}
	for _, zoneName := range f.subnetPages[page] {
		out.Subnets = append(out.Subnets, ec2types.Subnet{AvailabilityZone: aws.String(zoneName)})
	}
	if page+1 < len(f.subnetPages) {
		out.NextToken = aws.String(strings.Repeat("t", page+1))
	}
	return out, nil
}

// availableZone builds an available Availability Zone (zone-type
// "availability-zone") for the fake.
func availableZone(name, id string) ec2types.AvailabilityZone {
	return ec2types.AvailabilityZone{
		ZoneName: aws.String(name),
		ZoneId:   aws.String(id),
		ZoneType: aws.String(azZoneTypeAvailabilityZone),
		State:    ec2types.AvailabilityZoneStateAvailable,
	}
}

func TestResolveAvailabilityZonesDefaultSubnetFilter(t *testing.T) {
	t.Parallel()

	fake := &fakeAvailabilityZonesAPI{
		zones: []ec2types.AvailabilityZone{
			availableZone("eu-west-1a", "euw1-az1"),
			availableZone("eu-west-1b", "euw1-az2"),
			availableZone("eu-west-1c", "euw1-az3"),
		},
		// Only two of the three zones have a default subnet, which is what
		// Fn::GetAZs returns.
		subnetPages: [][]string{{"eu-west-1c", "eu-west-1a"}},
	}

	got, _, err := resolveAvailabilityZones(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	assertStrings(t, "names", got.Names, []string{"eu-west-1a", "eu-west-1c"})
	assertStrings(t, "all_names", got.AllNames, []string{"eu-west-1a", "eu-west-1b", "eu-west-1c"})
	assertStrings(t, "zone_ids", got.ZoneIDs, []string{"euw1-az1", "euw1-az2", "euw1-az3"})

	if len(fake.lastSubnetFilters) != 1 {
		t.Fatalf("DescribeSubnets called with %d filters, want 1", len(fake.lastSubnetFilters))
	}
	filter := fake.lastSubnetFilters[0]
	if aws.ToString(filter.Name) != azDefaultForAZFilter {
		t.Errorf("DescribeSubnets filter name = %q, want %q", aws.ToString(filter.Name), azDefaultForAZFilter)
	}
	if len(filter.Values) != 1 || filter.Values[0] != "true" {
		t.Errorf("DescribeSubnets filter values = %v, want [true]", filter.Values)
	}
}

// TestResolveAvailabilityZonesFallback pins CloudFormation's documented
// EC2-VPC behaviour: with no default subnet anywhere, Fn::GetAZs returns
// every Availability Zone.
func TestResolveAvailabilityZonesFallback(t *testing.T) {
	t.Parallel()

	fake := &fakeAvailabilityZonesAPI{
		zones: []ec2types.AvailabilityZone{
			availableZone("eu-west-1a", "euw1-az1"),
			availableZone("eu-west-1b", "euw1-az2"),
		},
		subnetPages: nil,
	}

	got, _, err := resolveAvailabilityZones(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	assertStrings(t, "names", got.Names, []string{"eu-west-1a", "eu-west-1b"})
	assertStrings(t, "all_names", got.AllNames, []string{"eu-west-1a", "eu-west-1b"})
}

// TestResolveAvailabilityZonesFiltersAndSorts covers the three filtering
// rules (state, zone type, missing name) plus alphabetical ordering, and the
// index alignment of zone_ids with all_names.
func TestResolveAvailabilityZonesFiltersAndSorts(t *testing.T) {
	t.Parallel()

	fake := &fakeAvailabilityZonesAPI{
		zones: []ec2types.AvailabilityZone{
			// Deliberately out of alphabetical order.
			availableZone("us-east-1c", "use1-az3"),
			availableZone("us-east-1a", "use1-az1"),
			// Impaired/unavailable zones are excluded.
			{
				ZoneName: aws.String("us-east-1d"),
				ZoneId:   aws.String("use1-az4"),
				ZoneType: aws.String(azZoneTypeAvailabilityZone),
				State:    ec2types.AvailabilityZoneStateImpaired,
			},
			// Local Zones and Wavelength Zones are not Availability Zones.
			{
				ZoneName: aws.String("us-east-1-bos-1a"),
				ZoneId:   aws.String("use1-bos1-az1"),
				ZoneType: aws.String("local-zone"),
				State:    ec2types.AvailabilityZoneStateAvailable,
			},
			{
				ZoneName: aws.String("us-east-1-wl1-bos-wlz-1"),
				ZoneId:   aws.String("use1-wl1-bos-wlz1"),
				ZoneType: aws.String("wavelength-zone"),
				State:    ec2types.AvailabilityZoneStateAvailable,
			},
			// A nameless zone cannot be used and must not panic.
			{
				ZoneId:   aws.String("use1-az9"),
				ZoneType: aws.String(azZoneTypeAvailabilityZone),
				State:    ec2types.AvailabilityZoneStateAvailable,
			},
			availableZone("us-east-1b", "use1-az2"),
		},
		subnetPages: [][]string{{"us-east-1a", "us-east-1b", "us-east-1c"}},
	}

	got, _, err := resolveAvailabilityZones(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	assertStrings(t, "all_names", got.AllNames, []string{"us-east-1a", "us-east-1b", "us-east-1c"})
	assertStrings(t, "zone_ids", got.ZoneIDs, []string{"use1-az1", "use1-az2", "use1-az3"})
	assertStrings(t, "names", got.Names, []string{"us-east-1a", "us-east-1b", "us-east-1c"})
}

// TestResolveAvailabilityZonesPaginatesSubnets checks that every
// DescribeSubnets page contributes to the default-subnet set.
func TestResolveAvailabilityZonesPaginatesSubnets(t *testing.T) {
	t.Parallel()

	fake := &fakeAvailabilityZonesAPI{
		zones: []ec2types.AvailabilityZone{
			availableZone("eu-west-1a", "euw1-az1"),
			availableZone("eu-west-1b", "euw1-az2"),
			availableZone("eu-west-1c", "euw1-az3"),
		},
		subnetPages: [][]string{{"eu-west-1a"}, {"eu-west-1c"}},
	}

	got, _, err := resolveAvailabilityZones(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	assertStrings(t, "names", got.Names, []string{"eu-west-1a", "eu-west-1c"})
	if fake.describeSubnetsCalls != 2 {
		t.Errorf("DescribeSubnets called %d times, want 2 (one per page)", fake.describeSubnetsCalls)
	}
}

func TestResolveAvailabilityZonesEmptyRegion(t *testing.T) {
	t.Parallel()

	fake := &fakeAvailabilityZonesAPI{}

	got, _, err := resolveAvailabilityZones(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	// Empty, never null: an empty list renders as [] in Terraform state.
	assertStrings(t, "names", got.Names, []string{})
	assertStrings(t, "all_names", got.AllNames, []string{})
	assertStrings(t, "zone_ids", got.ZoneIDs, []string{})
}

func TestResolveAvailabilityZonesErrors(t *testing.T) {
	t.Parallel()

	t.Run("DescribeAvailabilityZones failure", func(t *testing.T) {
		t.Parallel()

		fake := &fakeAvailabilityZonesAPI{zonesErr: errors.New("boom")}

		_, _, err := resolveAvailabilityZones(context.Background(), fake)
		if err == nil {
			t.Fatal("expected an error, got none")
		}
		if !strings.Contains(err.Error(), "boom") {
			t.Errorf("error %q does not wrap the underlying failure", err)
		}
		if fake.describeSubnetsCalls != 0 {
			t.Errorf("DescribeSubnets called %d times after a zones failure, want 0", fake.describeSubnetsCalls)
		}
	})

	t.Run("DescribeSubnets failure", func(t *testing.T) {
		t.Parallel()

		fake := &fakeAvailabilityZonesAPI{
			zones:      []ec2types.AvailabilityZone{availableZone("eu-west-1a", "euw1-az1")},
			subnetsErr: errors.New("access denied"),
		}

		_, _, err := resolveAvailabilityZones(context.Background(), fake)
		if err == nil {
			t.Fatal("expected an error, got none")
		}
		if !strings.Contains(err.Error(), "access denied") {
			t.Errorf("error %q does not wrap the underlying failure", err)
		}
	})

	t.Run("nil client", func(t *testing.T) {
		t.Parallel()

		_, _, err := resolveAvailabilityZones(context.Background(), nil)
		if err == nil {
			t.Fatal("expected an error with no EC2 client, got none")
		}
	})
}

// readAvailabilityZones runs the data source's Read with the given config
// model and returns the resulting state model plus the response.
func readAvailabilityZones(t *testing.T, d *AvailabilityZonesDataSource, config AvailabilityZonesDataSourceModel) (AvailabilityZonesDataSourceModel, *datasource.ReadResponse) {
	t.Helper()

	return readDataSource(t, d, config)
}

// nullAvailabilityZonesConfig returns a config model with every attribute
// null, i.e. `data "cfncompat_availability_zones" "x" {}`.
func nullAvailabilityZonesConfig() AvailabilityZonesDataSourceModel {
	return AvailabilityZonesDataSourceModel{
		Region:   types.StringNull(),
		Names:    types.ListNull(types.StringType),
		AllNames: types.ListNull(types.StringType),
		ZoneIds:  types.ListNull(types.StringType),
		Id:       types.StringNull(),
	}
}

// TestAvailabilityZonesDataSourceReadRegion pins the Fn::GetAZs region
// argument semantics: unset or "" means the provider's region, and an
// explicit region is the one the EC2 client is built for.
func TestAvailabilityZonesDataSourceReadRegion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		configRegion   types.String
		providerRegion string
		wantRegion     string
	}{
		{
			name:           "unset region uses the provider region",
			configRegion:   types.StringNull(),
			providerRegion: "eu-west-1",
			wantRegion:     "eu-west-1",
		},
		{
			name:           "empty region uses the provider region",
			configRegion:   types.StringValue(""),
			providerRegion: "eu-west-1",
			wantRegion:     "eu-west-1",
		},
		{
			name:           "explicit region overrides the provider region",
			configRegion:   types.StringValue("ap-southeast-2"),
			providerRegion: "eu-west-1",
			wantRegion:     "ap-southeast-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeAvailabilityZonesAPI{
				zones:       []ec2types.AvailabilityZone{availableZone(tt.wantRegion+"a", "az1")},
				subnetPages: [][]string{{tt.wantRegion + "a"}},
			}

			var gotClientRegion string
			d := &AvailabilityZonesDataSource{
				providerData: &ProviderData{Region: tt.providerRegion},
				newClient: func(region string) availabilityZonesAPI {
					gotClientRegion = region
					return fake
				},
			}

			config := nullAvailabilityZonesConfig()
			config.Region = tt.configRegion

			state, resp := readAvailabilityZones(t, d, config)
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
			}

			if gotClientRegion != tt.wantRegion {
				t.Errorf("EC2 client built for region %q, want %q", gotClientRegion, tt.wantRegion)
			}
			if state.Region.ValueString() != tt.wantRegion {
				t.Errorf("region = %q, want %q", state.Region.ValueString(), tt.wantRegion)
			}
			if state.Id.ValueString() != tt.wantRegion {
				t.Errorf("id = %q, want %q", state.Id.ValueString(), tt.wantRegion)
			}
		})
	}
}

func TestAvailabilityZonesDataSourceReadErrors(t *testing.T) {
	t.Parallel()

	t.Run("surfaces ConfigErr", func(t *testing.T) {
		t.Parallel()

		d := &AvailabilityZonesDataSource{
			providerData: &ProviderData{
				Region:    "eu-west-1",
				ConfigErr: errors.New("no valid credential sources found"),
			},
			newClient: func(string) availabilityZonesAPI {
				t.Fatal("newClient must not be called when the AWS config failed to resolve")
				return nil
			},
		}

		_, resp := readAvailabilityZones(t, d, nullAvailabilityZonesConfig())
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic when ProviderData.ConfigErr is set")
		}
		if !diagnosticsContain(resp.Diagnostics, "no valid credential sources found") {
			t.Errorf("diagnostics %v do not surface the underlying ConfigErr", resp.Diagnostics)
		}
	})

	t.Run("surfaces an unresolvable region", func(t *testing.T) {
		t.Parallel()

		d := &AvailabilityZonesDataSource{
			providerData: &ProviderData{Region: ""},
			newClient: func(string) availabilityZonesAPI {
				t.Fatal("newClient must not be called with no region")
				return nil
			},
		}

		_, resp := readAvailabilityZones(t, d, nullAvailabilityZonesConfig())
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic when no region could be resolved")
		}
		if !diagnosticsContain(resp.Diagnostics, "AWS_REGION") {
			t.Errorf("diagnostics %v do not explain how to set a region", resp.Diagnostics)
		}
	})

	t.Run("errors when the provider was never configured", func(t *testing.T) {
		t.Parallel()

		d := &AvailabilityZonesDataSource{}

		_, resp := readAvailabilityZones(t, d, nullAvailabilityZonesConfig())
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic when Configure never ran")
		}
	})

	t.Run("errors when no EC2 client factory was configured", func(t *testing.T) {
		t.Parallel()

		// Configured provider, resolvable region, but Configure never built
		// the factory -- a provider bug, and it must say so rather than
		// panic on a nil call.
		d := &AvailabilityZonesDataSource{providerData: &ProviderData{Region: "eu-west-1"}}

		_, resp := readAvailabilityZones(t, d, nullAvailabilityZonesConfig())
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic when newClient is nil")
		}
		if !diagnosticsContain(resp.Diagnostics, "client factory") {
			t.Errorf("diagnostics %v do not name the missing client factory", resp.Diagnostics)
		}
	})

	t.Run("surfaces an EC2 failure", func(t *testing.T) {
		t.Parallel()

		fake := &fakeAvailabilityZonesAPI{zonesErr: errors.New("UnauthorizedOperation")}
		d := &AvailabilityZonesDataSource{
			providerData: &ProviderData{Region: "eu-west-1"},
			newClient:    func(string) availabilityZonesAPI { return fake },
		}

		_, resp := readAvailabilityZones(t, d, nullAvailabilityZonesConfig())
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic when DescribeAvailabilityZones fails")
		}
		if !diagnosticsContain(resp.Diagnostics, "UnauthorizedOperation") {
			t.Errorf("diagnostics %v do not surface the EC2 failure", resp.Diagnostics)
		}
	})
}

// TestResolveAvailabilityZonesRepeatedSubnetToken guards the DescribeSubnets
// pagination loop against a response that hands back the token it was called
// with, which would otherwise page forever.
func TestResolveAvailabilityZonesRepeatedSubnetToken(t *testing.T) {
	t.Parallel()

	fake := &fakeAvailabilityZonesAPI{
		zones:           []ec2types.AvailabilityZone{availableZone("eu-west-1a", "euw1-az1")},
		echoSubnetToken: true,
	}

	_, _, err := resolveAvailabilityZones(context.Background(), fake)
	if err == nil {
		t.Fatal("expected an error when DescribeSubnets repeats its pagination token, got none")
	}
	if !strings.Contains(err.Error(), "pagination token") {
		t.Errorf("error %q does not explain the repeated pagination token", err)
	}
	// Two calls: the first hands out "stuck", the second echoes it back and
	// is caught. Anything more means the loop is not bounded.
	if fake.describeSubnetsCalls != 2 {
		t.Errorf("DescribeSubnets called %d times, want 2", fake.describeSubnetsCalls)
	}
}

// TestResolveAvailabilityZonesSubnetsDenied pins the degradation rule: a
// DescribeSubnets call the caller is not allowed to make must not break
// Fn::GetAZs -- names falls back to every available zone, with a warning
// naming the missing permission.
func TestResolveAvailabilityZonesSubnetsDenied(t *testing.T) {
	t.Parallel()

	denials := []struct {
		code    string
		message string
	}{
		{code: "UnauthorizedOperation", message: "You are not authorized to perform this operation."},
		{code: "AccessDenied", message: "Access denied"},
		{code: "AccessDeniedException", message: "Access denied"},
	}

	for _, denial := range denials {
		t.Run(denial.code, func(t *testing.T) {
			t.Parallel()

			fake := &fakeAvailabilityZonesAPI{
				zones: []ec2types.AvailabilityZone{
					availableZone("eu-west-1a", "euw1-az1"),
					availableZone("eu-west-1b", "euw1-az2"),
				},
				subnetsErr: &smithy.GenericAPIError{Code: denial.code, Message: denial.message},
			}

			got, diags, err := resolveAvailabilityZones(context.Background(), fake)
			if err != nil {
				t.Fatalf("a denied DescribeSubnets must not fail the read: %s", err)
			}
			if diags.HasError() {
				t.Fatalf("expected a warning, got errors: %v", diags)
			}
			if diags.WarningsCount() != 1 {
				t.Fatalf("got %d warnings, want 1 (diags: %v)", diags.WarningsCount(), diags)
			}
			if !diagnosticsContain(diags, "ec2:DescribeSubnets") {
				t.Errorf("warning %v does not name the missing permission", diags)
			}
			if !diagnosticsContain(diags, "no Availability Zone has a default subnet") {
				t.Errorf("warning %v does not explain the CloudFormation fallback rule", diags)
			}

			assertStrings(t, "names", got.Names, []string{"eu-west-1a", "eu-west-1b"})
			assertStrings(t, "all_names", got.AllNames, []string{"eu-west-1a", "eu-west-1b"})
			assertNotAliased(t, got)
		})
	}
}

// TestResolveAvailabilityZonesOtherSubnetErrorsFail is the other half of the
// rule: only an authorization failure degrades: everything else is a real
// error.
func TestResolveAvailabilityZonesOtherSubnetErrorsFail(t *testing.T) {
	t.Parallel()

	fake := &fakeAvailabilityZonesAPI{
		zones:      []ec2types.AvailabilityZone{availableZone("eu-west-1a", "euw1-az1")},
		subnetsErr: &smithy.GenericAPIError{Code: "RequestLimitExceeded", Message: "throttled"},
	}

	_, _, err := resolveAvailabilityZones(context.Background(), fake)
	if err == nil {
		t.Fatal("expected a non-authorization DescribeSubnets failure to be an error, got none")
	}
	if !strings.Contains(err.Error(), "RequestLimitExceeded") {
		t.Errorf("error %q does not wrap the underlying failure", err)
	}
}

// TestResolveAvailabilityZonesFallbackDoesNotAlias pins that the
// no-default-subnet fallback copies all_names instead of sharing its backing
// array, so appending to one list can never be visible in the other.
func TestResolveAvailabilityZonesFallbackDoesNotAlias(t *testing.T) {
	t.Parallel()

	fake := &fakeAvailabilityZonesAPI{
		zones: []ec2types.AvailabilityZone{
			availableZone("eu-west-1a", "euw1-az1"),
			availableZone("eu-west-1b", "euw1-az2"),
		},
	}

	got, _, err := resolveAvailabilityZones(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	assertNotAliased(t, got)
}

// assertNotAliased reports whether Names and AllNames share a backing array.
func assertNotAliased(t *testing.T, values availabilityZoneValues) {
	t.Helper()

	if len(values.Names) == 0 || len(values.AllNames) == 0 {
		return
	}
	if &values.Names[0] == &values.AllNames[0] {
		t.Error("names and all_names alias the same backing array")
	}
}

// TestAvailabilityZonesDataSourceConfigure mirrors
// TestCustomResourceConfigure.
func TestAvailabilityZonesDataSourceConfigure(t *testing.T) {
	t.Parallel()

	t.Run("nil ProviderData is a no-op", func(t *testing.T) {
		t.Parallel()

		d := &AvailabilityZonesDataSource{}
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: nil}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.providerData != nil || d.newClient != nil {
			t.Error("expected the data source to stay unconfigured when ProviderData is nil")
		}
	})

	t.Run("unexpected ProviderData type errors", func(t *testing.T) {
		t.Parallel()

		d := &AvailabilityZonesDataSource{}
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-provider-data"}, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic for an unexpected ProviderData type")
		}
		if d.newClient != nil {
			t.Error("expected no EC2 client factory for an unexpected ProviderData type")
		}
	})

	t.Run("ConfigErr is deferred to Read", func(t *testing.T) {
		t.Parallel()

		d := &AvailabilityZonesDataSource{}
		resp := &datasource.ConfigureResponse{}
		pd := &ProviderData{ConfigErr: errors.New("no credentials found")}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: pd}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.providerData != pd {
			t.Error("expected ProviderData to be retained so Read can surface ConfigErr")
		}
		if d.newClient != nil {
			t.Error("expected no EC2 client factory when ConfigErr is set")
		}
	})

	t.Run("builds an EC2 client factory", func(t *testing.T) {
		t.Parallel()

		d := &AvailabilityZonesDataSource{}
		resp := &datasource.ConfigureResponse{}
		pd := &ProviderData{
			Region:    "eu-west-1",
			Endpoints: EndpointsConfig{EC2: "http://localhost:4566"},
		}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: pd}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.providerData != pd {
			t.Error("expected ProviderData to be retained")
		}
		if d.newClient == nil {
			t.Fatal("expected an EC2 client factory to be built")
		}
		if d.newClient("ap-southeast-2") == nil {
			t.Error("expected the factory to build a client for an arbitrary region")
		}
	})
}

// assertStrings compares a []string against the expected value, reporting
// the whole slice on mismatch.
func assertStrings(t *testing.T, what string, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s = %v, want %v", what, got, want)
			return
		}
	}
}

// TestAccAvailabilityZonesDataSource reads cfncompat_availability_zones
// against real AWS with ec2:DescribeAvailabilityZones and
// ec2:DescribeSubnets. It creates nothing.
//
// Like TestAccCustomResource it is opt-in on top of TF_ACC, because CI runs
// the acceptance suite with TF_ACC=1 and no AWS credentials: set
// CFNCOMPAT_TEST_AWS=1 with credentials resolvable the usual way (e.g.
// aws-vault exec).
//
// The explicit-region step assumes the commercial partition: eu-west-1 is
// hard-coded, so the credentials must be able to reach it (an aws-cn or
// aws-us-gov role cannot).
func TestAccAvailabilityZonesDataSource(t *testing.T) {
	if os.Getenv("CFNCOMPAT_TEST_AWS") != "1" {
		t.Skip("set CFNCOMPAT_TEST_AWS=1 (with TF_ACC=1 and AWS credentials) to run this acceptance test")
	}

	rtresource.Test(t, rtresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []rtresource.TestStep{
			{
				// Fn::GetAZs "" -- the provider's own region.
				Config: `
data "cfncompat_availability_zones" "current" {}

output "first_az" {
  value = provider::cfncompat::select(0, data.cfncompat_availability_zones.current.names)
}

output "az_region" {
  value = data.cfncompat_availability_zones.current.region
}
`,
				Check: rtresource.ComposeAggregateTestCheckFunc(
					rtresource.TestCheckResourceAttrSet("data.cfncompat_availability_zones.current", "names.0"),
					rtresource.TestCheckResourceAttrSet("data.cfncompat_availability_zones.current", "all_names.0"),
					rtresource.TestCheckResourceAttrSet("data.cfncompat_availability_zones.current", "zone_ids.0"),
					rtresource.TestCheckResourceAttrPair(
						"data.cfncompat_availability_zones.current", "id",
						"data.cfncompat_availability_zones.current", "region",
					),
					// The first zone name always starts with its region.
					testAccCheckOutputHasPrefixOfOutput("first_az", "az_region"),
				),
			},
			{
				// Fn::GetAZs "" spelled out: an explicitly empty region is
				// AWS::Region, and both region and id echo the region that
				// was actually queried.
				Config: `
data "cfncompat_availability_zones" "empty" {
  region = ""
}

output "empty_region" {
  value = data.cfncompat_availability_zones.empty.region
}

output "empty_first_az" {
  value = provider::cfncompat::select(0, data.cfncompat_availability_zones.empty.names)
}
`,
				Check: rtresource.ComposeAggregateTestCheckFunc(
					rtresource.TestMatchResourceAttr("data.cfncompat_availability_zones.empty", "region", regexp.MustCompile(`^[a-z0-9-]+$`)),
					rtresource.TestCheckResourceAttrPair(
						"data.cfncompat_availability_zones.empty", "id",
						"data.cfncompat_availability_zones.empty", "region",
					),
					rtresource.TestCheckResourceAttrSet("data.cfncompat_availability_zones.empty", "names.0"),
					testAccCheckOutputHasPrefixOfOutput("empty_first_az", "empty_region"),
				),
			},
			{
				// Fn::GetAZs "eu-west-1" -- an explicit region, which need
				// not be the provider's.
				Config: `
data "cfncompat_availability_zones" "euw1" {
  region = "eu-west-1"
}
`,
				Check: rtresource.ComposeAggregateTestCheckFunc(
					rtresource.TestCheckResourceAttr("data.cfncompat_availability_zones.euw1", "region", "eu-west-1"),
					rtresource.TestCheckResourceAttr("data.cfncompat_availability_zones.euw1", "id", "eu-west-1"),
					rtresource.TestMatchResourceAttr("data.cfncompat_availability_zones.euw1", "names.0", regexp.MustCompile(`^eu-west-1[a-z]$`)),
					rtresource.TestMatchResourceAttr("data.cfncompat_availability_zones.euw1", "all_names.0", regexp.MustCompile(`^eu-west-1[a-z]$`)),
					rtresource.TestMatchResourceAttr("data.cfncompat_availability_zones.euw1", "zone_ids.0", regexp.MustCompile(`^euw1-az[0-9]+$`)),
				),
			},
		},
	})
}

// testAccCheckOutputHasPrefixOfOutput asserts that the value of one
// Terraform output starts with the value of another -- used to check that
// the first Availability Zone name is prefixed with its region without
// hard-coding which region the acceptance test runs in.
func testAccCheckOutputHasPrefixOfOutput(name, prefixName string) rtresource.TestCheckFunc {
	return func(s *rtterraform.State) error {
		value, err := testAccOutputString(s, name)
		if err != nil {
			return err
		}
		prefix, err := testAccOutputString(s, prefixName)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(value, prefix) {
			return fmt.Errorf("output %q = %q, expected it to start with output %q = %q", name, value, prefixName, prefix)
		}
		return nil
	}
}

// testAccOutputString reads a root-module string output from Terraform state.
func testAccOutputString(s *rtterraform.State, name string) (string, error) {
	ms := s.RootModule()
	out, ok := ms.Outputs[name]
	if !ok {
		return "", fmt.Errorf("output %q not found", name)
	}
	value, ok := out.Value.(string)
	if !ok {
		return "", fmt.Errorf("output %q is %T, expected a string", name, out.Value)
	}
	return value, nil
}
