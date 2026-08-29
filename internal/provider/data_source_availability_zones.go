// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure AvailabilityZonesDataSource satisfies the framework data source
// interfaces.
var _ datasource.DataSource = &AvailabilityZonesDataSource{}
var _ datasource.DataSourceWithConfigure = &AvailabilityZonesDataSource{}

// azZoneTypeAvailabilityZone is the EC2 zone-type value for a true
// Availability Zone, as opposed to a Local Zone ("local-zone") or a
// Wavelength Zone ("wavelength-zone"). DescribeAvailabilityZones returns
// opted-in Local/Wavelength Zones alongside real AZs, but Fn::GetAZs is
// specified as "an array that lists Availability Zones for a specified
// Region", and CloudFormation's own examples only ever return
// <region><letter> zones -- so this data source keeps only this zone type.
const azZoneTypeAvailabilityZone = "availability-zone"

// azDefaultForAZFilter is the DescribeSubnets filter that selects the
// default subnet of each Availability Zone. It implements the documented
// EC2-VPC behaviour of Fn::GetAZs: "returns only Availability Zones that
// have a default subnet unless none of the Availability Zones has a default
// subnet; in that case, all Availability Zones are returned".
const azDefaultForAZFilter = "default-for-az"

// azPlatformVPC is the value of the EC2 "supported-platforms" account
// attribute that means the account is on EC2-VPC, i.e. the platform whose
// default-subnet restriction Fn::GetAZs applies. CloudFormation reads the
// same attribute (which is why Fn::GetAZs is documented as needing
// ec2:DescribeAccountAttributes) to decide between the EC2-Classic and the
// EC2-VPC behaviour.
//
// EC2-Classic was retired in August 2022, so every real account reports VPC
// today and an account reporting no supported platform at all is treated as
// EC2-VPC. The EC2-Classic branch exists for fidelity with the documented
// contract, not because it is reachable.
const azPlatformVPC = "VPC"

// availabilityZonesAPI is the subset of the EC2 API client used to resolve
// Fn::GetAZs. Implemented by *ec2.Client; faked in tests so the data
// source's logic runs without AWS.
type availabilityZonesAPI interface {
	DescribeAvailabilityZones(ctx context.Context, params *ec2.DescribeAvailabilityZonesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAvailabilityZonesOutput, error)
	DescribeAccountAttributes(ctx context.Context, params *ec2.DescribeAccountAttributesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAccountAttributesOutput, error)
	DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
}

// NewAvailabilityZonesDataSource returns a new instance of the
// cfncompat_availability_zones data source.
func NewAvailabilityZonesDataSource() datasource.DataSource {
	return &AvailabilityZonesDataSource{}
}

// AvailabilityZonesDataSource implements CloudFormation's Fn::GetAZs
// intrinsic function as a data source.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-getavailabilityzones.html
type AvailabilityZonesDataSource struct {
	// providerData is nil until Configure has run with non-nil
	// req.ProviderData (e.g. during schema/validation-only requests).
	providerData *ProviderData
	// newClient builds an EC2 client bound to a specific region (the data
	// source's `region` argument may differ from the provider's). It is a
	// field so unit tests can substitute a fake and assert which region the
	// client was built for.
	newClient func(region string) availabilityZonesAPI
}

// AvailabilityZonesDataSourceModel is the Terraform data model for
// cfncompat_availability_zones.
type AvailabilityZonesDataSourceModel struct {
	Region   types.String `tfsdk:"region"`
	Names    types.List   `tfsdk:"names"`
	AllNames types.List   `tfsdk:"all_names"`
	ZoneIds  types.List   `tfsdk:"zone_ids"`
	Id       types.String `tfsdk:"id"`
}

func (d *AvailabilityZonesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_availability_zones"
}

func (d *AvailabilityZonesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Implements CloudFormation's Fn::GetAZs intrinsic function: lists the Availability Zones " +
			"of a region, in alphabetical order, restricted (as CloudFormation does) to zones that have a " +
			"default subnet. Requires resolvable AWS credentials and region.",
		MarkdownDescription: "Implements CloudFormation's " +
			"[`Fn::GetAZs`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-getavailabilityzones.html) " +
			"intrinsic function: lists the Availability Zones of a region, in alphabetical order, restricted " +
			"(exactly as CloudFormation does) to zones that have a default subnet.\n\n" +
			"Combine it with `provider::cfncompat::select` for the `Fn::Select` + `Fn::GetAZs` pattern:\n\n" +
			"```terraform\n" +
			"data \"cfncompat_availability_zones\" \"current\" {}\n\n" +
			"locals {\n" +
			"  az0 = provider::cfncompat::select(0, data.cfncompat_availability_zones.current.names)\n" +
			"}\n" +
			"```\n\n" +
			"Unlike the provider-defined functions, this data source **requires resolvable AWS " +
			"credentials and region**, and the same permissions CloudFormation documents for " +
			"`Fn::GetAZs`: `ec2:DescribeAvailabilityZones` and `ec2:DescribeAccountAttributes`, plus " +
			"`ec2:DescribeSubnets` for the EC2-VPC default-subnet restriction. Every one of them is " +
			"required: a denied call fails the read rather than silently changing `names`.",
		Attributes: map[string]schema.Attribute{
			"region": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "The region to list Availability Zones for, i.e. the Fn::GetAZs argument. An " +
					"empty string or an unset value means the provider's own region, exactly as an empty " +
					"string means AWS::Region in CloudFormation. Echoed back as the resolved region.",
				MarkdownDescription: "The region to list Availability Zones for, i.e. the `Fn::GetAZs` " +
					"argument. An empty string or an unset value means the provider's own region -- exactly as " +
					"[the `Fn::GetAZs` reference](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-getavailabilityzones.html) " +
					"specifies that \"specifying an empty string is equivalent to specifying `AWS::Region`\". " +
					"Echoed back as the region that was actually queried.",
			},
			"names": schema.ListAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "The Fn::GetAZs value: the available Availability Zones of the region that have " +
					"a default subnet, alphabetically. Falls back to every available zone (all_names) when no " +
					"zone has a default subnet -- CloudFormation's documented EC2-VPC behaviour.",
				MarkdownDescription: "**The `Fn::GetAZs` value**: the `available` Availability Zones of the " +
					"region that have a default subnet, in alphabetical order.\n\n" +
					"CloudFormation documents this exact EC2-VPC behaviour: *\"the `Fn::GetAZs` function " +
					"returns only Availability Zones that have a default subnet unless none of the " +
					"Availability Zones has a default subnet; in that case, all Availability Zones are " +
					"returned\"*. The default-subnet set comes from `DescribeSubnets` with the " +
					"`default-for-az=true` filter, and this attribute falls back to `all_names` when that set " +
					"is empty (e.g. an account whose default VPC has been deleted).\n\n" +
					"The restriction is EC2-VPC-only, so -- again as CloudFormation does -- the account's " +
					"`supported-platforms` attribute is read first (`ec2:DescribeAccountAttributes`) and the " +
					"`DescribeSubnets` call is skipped entirely for an EC2-Classic-only account, whose `names` " +
					"is then every available zone. EC2-Classic was retired in August 2022, so in practice every " +
					"account takes the EC2-VPC path.\n\n" +
					"CloudFormation does not guarantee an order; this data source always sorts " +
					"alphabetically, so `provider::cfncompat::select(0, ...)` is stable across plans.",
			},
			"all_names": schema.ListAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "Every available Availability Zone of the region, alphabetically, whether or " +
					"not it has a default subnet; Local Zones and Wavelength Zones are excluded. names is " +
					"filtered from it.",
				MarkdownDescription: "Every `available` Availability Zone of the region, in alphabetical " +
					"order, whether or not it has a default subnet. `names` is filtered from this list.\n\n" +
					"Zones whose state is not `available` are excluded, as are opted-in Local Zones " +
					"(`zone-type = local-zone`) and Wavelength Zones (`zone-type = wavelength-zone`): " +
					"`Fn::GetAZs` returns Availability Zones only.",
			},
			"zone_ids": schema.ListAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "The zone IDs (e.g. \"use1-az1\") of all_names, in the same order, so " +
					"all_names[i] and zone_ids[i] describe the same zone.",
				MarkdownDescription: "The zone IDs (e.g. `use1-az1`) of `all_names`, in the same order: " +
					"`all_names[i]` and `zone_ids[i]` always describe the same zone. Unlike zone *names*, zone " +
					"IDs are consistent across AWS accounts.",
			},
			"id": schema.StringAttribute{
				Computed:            true,
				Description:         "Identifier for this data source: the region that was queried.",
				MarkdownDescription: "Identifier for this data source: the region that was queried.",
			},
		},
	}
}

func (d *AvailabilityZonesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// ProviderData may be nil, e.g. during validation-only requests that
	// occur before the provider has been configured.
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *provider.ProviderData, got: %T. This is a bug in the cfncompat provider; please report it.", req.ProviderData),
		)
		return
	}

	d.providerData = pd

	// A failed AWS configuration is not an error here: it is surfaced by
	// Read, so that a configuration that never reads this data source keeps
	// working with an unconfigured provider (see ProviderData.ConfigErr).
	if pd.ConfigErr != nil {
		return
	}

	d.newClient = func(region string) availabilityZonesAPI {
		return ec2.NewFromConfig(pd.AwsConfig, func(o *ec2.Options) {
			// Fn::GetAZs takes a region argument that need not be the
			// provider's own, so the client is built per read.
			o.Region = region
			if pd.Endpoints.EC2 != "" {
				o.BaseEndpoint = aws.String(pd.Endpoints.EC2)
			}
		})
	}
}

func (d *AvailabilityZonesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model AvailabilityZonesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if d.providerData == nil {
		resp.Diagnostics.AddError(
			"cfncompat_availability_zones Not Configured",
			"the cfncompat provider was not configured, so cfncompat_availability_zones cannot list "+
				"Availability Zones. This is a bug in the cfncompat provider; please report it.",
		)
		return
	}

	if d.providerData.ConfigErr != nil {
		resp.Diagnostics.AddError(
			"AWS Configuration Required",
			"cfncompat_availability_zones requires resolvable AWS credentials/configuration to call EC2 "+
				"DescribeAvailabilityZones, but the provider could not resolve its AWS configuration: "+
				d.providerData.ConfigErr.Error(),
		)
		return
	}

	// An empty or unset `region` means AWS::Region, per the Fn::GetAZs
	// reference.
	region := model.Region.ValueString()
	if region == "" {
		region = d.providerData.Region
	}
	if region == "" {
		resp.Diagnostics.AddError(
			"No AWS Region Resolved",
			"cfncompat_availability_zones needs a region to list Availability Zones for, but `region` was "+
				"not set on the data source and the cfncompat provider resolved no region of its own: set "+
				"`region` on either, or the AWS_REGION/AWS_DEFAULT_REGION environment variable.",
		)
		return
	}

	if d.newClient == nil {
		resp.Diagnostics.AddError(
			"cfncompat_availability_zones Not Configured",
			"no EC2 client factory was configured. This is a bug in the cfncompat provider; please report it.",
		)
		return
	}

	zones, err := resolveAvailabilityZones(ctx, d.newClient(region))
	if err != nil {
		resp.Diagnostics.AddError("Failed to List Availability Zones", err.Error())
		return
	}

	names, diags := types.ListValueFrom(ctx, types.StringType, zones.Names)
	resp.Diagnostics.Append(diags...)
	allNames, diags := types.ListValueFrom(ctx, types.StringType, zones.AllNames)
	resp.Diagnostics.Append(diags...)
	zoneIDs, diags := types.ListValueFrom(ctx, types.StringType, zones.ZoneIDs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	model.Region = types.StringValue(region)
	model.Names = names
	model.AllNames = allNames
	model.ZoneIds = zoneIDs
	model.Id = types.StringValue(region)

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

// availabilityZoneValues holds the computed Fn::GetAZs values. AllNames and
// ZoneIDs are index-aligned; Names is a (possibly equal) subset of AllNames.
// All three are non-nil, so they render as empty lists rather than null.
type availabilityZoneValues struct {
	Names    []string
	AllNames []string
	ZoneIDs  []string
}

// resolveAvailabilityZones computes the Fn::GetAZs values for the region the
// client is bound to. It holds all of the data source's logic, taking the
// EC2 client as a narrow interface so it can be unit tested with a fake.
func resolveAvailabilityZones(ctx context.Context, client availabilityZonesAPI) (availabilityZoneValues, error) {
	if client == nil {
		return availabilityZoneValues{}, errors.New("no EC2 client was configured (this is a bug in the cfncompat provider; please report it)")
	}

	// No server-side filters: the zone state and zone type are filtered
	// below so that this one response is the single source of truth for both
	// all_names and zone_ids, and so the filtering rules are exercised by
	// unit tests rather than by EC2.
	out, err := client.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{})
	if err != nil {
		return availabilityZoneValues{}, fmt.Errorf("calling EC2 DescribeAvailabilityZones: %w", err)
	}

	type zone struct {
		name string
		id   string
	}
	var zones []zone
	if out != nil {
		for _, az := range out.AvailabilityZones {
			if az.ZoneName == nil || *az.ZoneName == "" {
				continue
			}
			if az.State != ec2types.AvailabilityZoneStateAvailable {
				continue
			}
			if az.ZoneType == nil || *az.ZoneType != azZoneTypeAvailabilityZone {
				continue
			}
			zones = append(zones, zone{name: *az.ZoneName, id: aws.ToString(az.ZoneId)})
		}
	}

	// CloudFormation does not guarantee an order; sorting alphabetically
	// keeps Fn::Select(n, Fn::GetAZs("")) stable across plans.
	sort.Slice(zones, func(i, j int) bool { return zones[i].name < zones[j].name })

	values := availabilityZoneValues{
		Names:    []string{},
		AllNames: make([]string, 0, len(zones)),
		ZoneIDs:  make([]string, 0, len(zones)),
	}
	for _, z := range zones {
		values.AllNames = append(values.AllNames, z.name)
		values.ZoneIDs = append(values.ZoneIDs, z.id)
	}

	// The default-subnet restriction is the EC2-VPC behaviour of Fn::GetAZs,
	// so -- exactly as CloudFormation does -- the account's platform decides
	// whether it applies at all.
	onVPC, err := accountIsOnVPCPlatform(ctx, client)
	if err != nil {
		return availabilityZoneValues{}, err
	}
	if !onVPC {
		// EC2-Classic has no subnets, so there is nothing to restrict by and
		// DescribeSubnets is not called: every available zone is the answer.
		// Cloned so names and all_names never alias the same backing array.
		values.Names = slices.Clone(values.AllNames)
		return values, nil
	}

	defaultSubnetZones, err := availabilityZonesWithDefaultSubnet(ctx, client)
	if err != nil {
		return availabilityZoneValues{}, err
	}

	for _, name := range values.AllNames {
		if defaultSubnetZones[name] {
			values.Names = append(values.Names, name)
		}
	}
	// "...unless none of the Availability Zones has a default subnet; in
	// that case, all Availability Zones are returned."
	if len(values.Names) == 0 {
		// Cloned, so that names and all_names never alias the same backing
		// array (a later append to one would otherwise be visible in the
		// other).
		values.Names = slices.Clone(values.AllNames)
	}

	return values, nil
}

// accountIsOnVPCPlatform reports whether the account supports the EC2-VPC
// platform, from the EC2 "supported-platforms" account attribute -- the same
// check CloudFormation makes before applying the default-subnet restriction
// of Fn::GetAZs.
//
// An account that reports no supported platform at all (an empty or absent
// attribute) is treated as EC2-VPC: EC2-Classic was retired in August 2022,
// so VPC is what every real account reports, and the EC2-Classic branch
// exists for fidelity with the documented contract rather than because it is
// reachable.
func accountIsOnVPCPlatform(ctx context.Context, client availabilityZonesAPI) (bool, error) {
	out, err := client.DescribeAccountAttributes(ctx, &ec2.DescribeAccountAttributesInput{
		AttributeNames: []ec2types.AccountAttributeName{ec2types.AccountAttributeNameSupportedPlatforms},
	})
	if err != nil {
		return false, fmt.Errorf(
			"calling EC2 DescribeAccountAttributes to read the account's supported platforms "+
				"(Fn::GetAZs requires the ec2:DescribeAccountAttributes permission): %w", err)
	}
	if out == nil {
		return true, nil
	}

	sawPlatform := false
	for _, attr := range out.AccountAttributes {
		if aws.ToString(attr.AttributeName) != string(ec2types.AccountAttributeNameSupportedPlatforms) {
			continue
		}
		for _, value := range attr.AttributeValues {
			platform := aws.ToString(value.AttributeValue)
			if platform == "" {
				continue
			}
			sawPlatform = true
			if platform == azPlatformVPC {
				return true, nil
			}
		}
	}

	// Only an account that positively reports platforms, none of which is
	// VPC, is EC2-Classic-only.
	return !sawPlatform, nil
}

// availabilityZonesWithDefaultSubnet returns the set of zone names that have
// a default subnet, i.e. the EC2-VPC restriction Fn::GetAZs applies.
func availabilityZonesWithDefaultSubnet(ctx context.Context, client availabilityZonesAPI) (map[string]bool, error) {
	found := map[string]bool{}

	input := &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{{
			Name:   aws.String(azDefaultForAZFilter),
			Values: []string{"true"},
		}},
	}

	// Every pagination token the loop has already sent, so a service
	// response that points back at one is caught as a cycle instead of
	// paging forever.
	seenTokens := map[string]bool{}

	for {
		out, err := client.DescribeSubnets(ctx, input)
		if err != nil {
			return nil, fmt.Errorf(
				"calling EC2 DescribeSubnets to find default subnets (Fn::GetAZs requires the "+
					"ec2:DescribeSubnets permission on EC2-VPC accounts): %w", err)
		}
		if out == nil {
			break
		}

		for _, subnet := range out.Subnets {
			if subnet.AvailabilityZone != nil && *subnet.AvailabilityZone != "" {
				found[*subnet.AvailabilityZone] = true
			}
		}

		next := aws.ToString(out.NextToken)
		if next == "" {
			break
		}
		// A page pointing at a token the loop has already followed -- the
		// token it was just called with, or one from any earlier page -- is a
		// pagination cycle that would page forever; treat it as a broken
		// response rather than looping.
		if seenTokens[next] {
			return nil, fmt.Errorf(
				"EC2 DescribeSubnets returned the pagination token %q a second time: a pagination cycle that would page forever",
				next,
			)
		}
		seenTokens[next] = true
		input.NextToken = out.NextToken
	}

	return found, nil
}
