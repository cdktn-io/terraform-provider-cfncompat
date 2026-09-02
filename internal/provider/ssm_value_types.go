// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
)

// This file holds the CloudFormation-supplied parameter type table shared by
// the three SSM parameter data sources. It is the provider-side counterpart of
// the inner type of `AWS::SSM::Parameter::Value<...>`: CloudFormation resolves
// the Parameter Store value and then validates it as the declared type, and
// these data sources do the same.
//
// Types are taken verbatim from "CloudFormation-supplied parameter types"
// (cloudformation-supplied-parameter-types.html): ten AWS-specific scalar
// types, and a List<> form of nine of them -- CloudFormation supports no
// List<AWS::EC2::KeyPair::KeyName>.

// CFN value type names. Declared as constants so the data sources, the
// validators and the tests all name the same strings.
const (
	cfnValueTypeString             = "String"
	cfnValueTypeListString         = "List<String>"
	cfnValueTypeCommaDelimitedList = "CommaDelimitedList"

	cfnValueTypeAvailabilityZoneName   = "AWS::EC2::AvailabilityZone::Name"
	cfnValueTypeImageID                = "AWS::EC2::Image::Id"
	cfnValueTypeInstanceID             = "AWS::EC2::Instance::Id"
	cfnValueTypeKeyPairKeyName         = "AWS::EC2::KeyPair::KeyName"
	cfnValueTypeSecurityGroupGroupName = "AWS::EC2::SecurityGroup::GroupName"
	cfnValueTypeSecurityGroupID        = "AWS::EC2::SecurityGroup::Id"
	cfnValueTypeSubnetID               = "AWS::EC2::Subnet::Id"
	cfnValueTypeVolumeID               = "AWS::EC2::Volume::Id"
	cfnValueTypeVPCID                  = "AWS::EC2::VPC::Id"
	cfnValueTypeHostedZoneID           = "AWS::Route53::HostedZone::Id"
)

// ec2ResourceIDPattern is the shared shape of an EC2 resource identifier: the
// resource's prefix plus either the legacy 8 or the current 17 hexadecimal
// characters.
func ec2ResourceIDPattern(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`^` + prefix + `-([0-9a-f]{8}|[0-9a-f]{17})$`)
}

// cfnValueTypeSpec describes one CloudFormation-supplied parameter type: the
// syntactic shape every value must have, and -- for the AWS-specific types --
// the API call CloudFormation makes to assert the value actually exists in the
// account and region.
type cfnValueTypeSpec struct {
	// Name is the CloudFormation type name, e.g. "AWS::EC2::Image::Id".
	Name string
	// Pattern, when non-nil, every value must match. A nil Pattern means the
	// type places no syntactic constraint on the value (plain String).
	Pattern *regexp.Regexp
	// PatternDescription describes Pattern in a diagnostic, e.g.
	// "ami- followed by 8 or 17 hexadecimal characters".
	PatternDescription string
	// IAMPermission is the IAM action the existence check needs, empty when
	// the type has no existence check.
	IAMPermission string
	// exists asserts that every value in values exists. It is nil for the
	// plain String type, which CloudFormation does not validate either.
	exists func(ctx context.Context, v *cfnTypeValidator, values []string) error
}

// HasExistenceCheck reports whether this type has an AWS-side existence check,
// i.e. whether `validate = true` costs an API call.
func (s cfnValueTypeSpec) HasExistenceCheck() bool { return s.exists != nil }

// cfnScalarValueTypes is every inner type valid inside
// AWS::SSM::Parameter::Value<...> for a scalar (String-backed) parameter.
var cfnScalarValueTypes = buildScalarValueTypes()

func buildScalarValueTypes() map[string]cfnValueTypeSpec {
	specs := []cfnValueTypeSpec{
		{
			Name: cfnValueTypeString,
		},
		{
			Name: cfnValueTypeAvailabilityZoneName,
			// us-east-1a, eu-west-1c, us-gov-west-1a, and the longer
			// Local Zone names such as us-east-1-bos-1a.
			Pattern:            regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)+-[0-9]+[a-z]$`),
			PatternDescription: "an Availability Zone name such as \"us-east-1a\"",
			IAMPermission:      "ec2:DescribeAvailabilityZones",
			exists:             existsAvailabilityZoneNames,
		},
		{
			Name:               cfnValueTypeImageID,
			Pattern:            ec2ResourceIDPattern("ami"),
			PatternDescription: "\"ami-\" followed by 8 or 17 hexadecimal characters",
			IAMPermission:      "ec2:DescribeImages",
			exists:             existsImageIDs,
		},
		{
			Name:               cfnValueTypeInstanceID,
			Pattern:            ec2ResourceIDPattern("i"),
			PatternDescription: "\"i-\" followed by 8 or 17 hexadecimal characters",
			IAMPermission:      "ec2:DescribeInstances",
			exists:             existsInstanceIDs,
		},
		{
			Name: cfnValueTypeKeyPairKeyName,
			// EC2 key pair names are 1-255 ASCII characters.
			Pattern:            regexp.MustCompile(`^[\x20-\x7E]{1,255}$`),
			PatternDescription: "1 to 255 printable ASCII characters",
			IAMPermission:      "ec2:DescribeKeyPairs",
			exists:             existsKeyPairNames,
		},
		{
			Name:               cfnValueTypeSecurityGroupGroupName,
			Pattern:            regexp.MustCompile(`^[a-zA-Z0-9 ._\-:/()#,@\[\]+=&;{}!$*]{1,255}$`),
			PatternDescription: "1 to 255 characters from the EC2 security group name character set",
			IAMPermission:      "ec2:DescribeSecurityGroups",
			exists:             existsSecurityGroupNames,
		},
		{
			Name:               cfnValueTypeSecurityGroupID,
			Pattern:            ec2ResourceIDPattern("sg"),
			PatternDescription: "\"sg-\" followed by 8 or 17 hexadecimal characters",
			IAMPermission:      "ec2:DescribeSecurityGroups",
			exists:             existsSecurityGroupIDs,
		},
		{
			Name:               cfnValueTypeSubnetID,
			Pattern:            ec2ResourceIDPattern("subnet"),
			PatternDescription: "\"subnet-\" followed by 8 or 17 hexadecimal characters",
			IAMPermission:      "ec2:DescribeSubnets",
			exists:             existsSubnetIDs,
		},
		{
			Name:               cfnValueTypeVolumeID,
			Pattern:            ec2ResourceIDPattern("vol"),
			PatternDescription: "\"vol-\" followed by 8 or 17 hexadecimal characters",
			IAMPermission:      "ec2:DescribeVolumes",
			exists:             existsVolumeIDs,
		},
		{
			Name:               cfnValueTypeVPCID,
			Pattern:            ec2ResourceIDPattern("vpc"),
			PatternDescription: "\"vpc-\" followed by 8 or 17 hexadecimal characters",
			IAMPermission:      "ec2:DescribeVpcs",
			exists:             existsVPCIDs,
		},
		{
			Name: cfnValueTypeHostedZoneID,
			// Route 53 hosted zone ids are uppercase alphanumerics; the
			// "/hostedzone/" prefix some APIs return is not part of the id
			// CloudFormation validates.
			Pattern:            regexp.MustCompile(`^[A-Z0-9]{1,32}$`),
			PatternDescription: "1 to 32 uppercase alphanumeric characters (a Route 53 hosted zone ID, without the \"/hostedzone/\" prefix)",
			IAMPermission:      "route53:GetHostedZone",
			exists:             existsHostedZoneIDs,
		},
	}

	out := make(map[string]cfnValueTypeSpec, len(specs))
	for _, s := range specs {
		out[s.Name] = s
	}
	return out
}

// cfnListValueTypes is every inner type valid inside
// AWS::SSM::Parameter::Value<...> for a list (StringList-backed) parameter:
// List<String>, CommaDelimitedList, and List<T> for every AWS-specific scalar
// type except AWS::EC2::KeyPair::KeyName, which CloudFormation has no list
// form of.
var cfnListValueTypes = buildListValueTypes()

func buildListValueTypes() map[string]cfnValueTypeSpec {
	out := map[string]cfnValueTypeSpec{
		cfnValueTypeListString:         {Name: cfnValueTypeListString},
		cfnValueTypeCommaDelimitedList: {Name: cfnValueTypeCommaDelimitedList},
	}
	for name, spec := range cfnScalarValueTypes {
		if name == cfnValueTypeString || name == cfnValueTypeKeyPairKeyName {
			// CloudFormation has no List<String> via the scalar table (it is
			// spelled List<String> above) and no
			// List<AWS::EC2::KeyPair::KeyName> at all.
			continue
		}
		listName := "List<" + name + ">"
		listSpec := spec
		listSpec.Name = listName
		out[listName] = listSpec
	}
	return out
}

// sortedValueTypeNames returns the keys of a value-type table in a stable
// order, for diagnostics and documentation.
func sortedValueTypeNames(table map[string]cfnValueTypeSpec) []string {
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// cfnEC2ValidationAPI is the subset of the EC2 API used by the AWS-specific
// value-type existence checks -- the same Describe* calls CloudFormation makes
// when it validates an AWS::SSM::Parameter::Value<AWS::EC2::...> value.
// Implemented by *ec2.Client; faked in tests so the checks run without AWS.
type cfnEC2ValidationAPI interface {
	DescribeAvailabilityZones(ctx context.Context, params *ec2.DescribeAvailabilityZonesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAvailabilityZonesOutput, error)
	DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	DescribeKeyPairs(ctx context.Context, params *ec2.DescribeKeyPairsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeKeyPairsOutput, error)
	DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
}

// cfnRoute53ValidationAPI is the subset of the Route 53 API used by the
// AWS::Route53::HostedZone::Id existence check.
type cfnRoute53ValidationAPI interface {
	GetHostedZone(ctx context.Context, params *route53.GetHostedZoneInput, optFns ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error)
}

// cfnTypeValidator carries the clients the AWS-specific existence checks need.
// A nil field means the corresponding check cannot run and reports so rather
// than panicking.
type cfnTypeValidator struct {
	EC2     cfnEC2ValidationAPI
	Route53 cfnRoute53ValidationAPI
}

// validateCfnValueType applies the CloudFormation-supplied parameter type to
// resolved values: always the syntactic check, and -- when checkExistence is
// true and the type has one -- the AWS-side existence check.
//
// values is a single-element slice for a scalar type and the split elements
// for a list type, so both data sources share one implementation and the
// existence check is a single batched API call either way.
func validateCfnValueType(ctx context.Context, spec cfnValueTypeSpec, values []string, checkExistence bool, v *cfnTypeValidator) error {
	if spec.Pattern != nil {
		for i, value := range values {
			if spec.Pattern.MatchString(value) {
				continue
			}
			return fmt.Errorf(
				"the resolved value %s is not a valid %s: CloudFormation requires %s",
				describeValueAt(values, i), spec.Name, spec.PatternDescription,
			)
		}
	}

	if !checkExistence || spec.exists == nil {
		return nil
	}
	if len(values) == 0 {
		return nil
	}
	if v == nil {
		return fmt.Errorf(
			"cannot check that the resolved value exists as a %s: no AWS clients were configured "+
				"(this is a bug in the cfncompat provider; please report it)", spec.Name)
	}
	return spec.exists(ctx, v, dedupe(values))
}

// describeValueAt renders a value for a diagnostic, naming its list position
// when it came from a list.
func describeValueAt(values []string, i int) string {
	if len(values) == 1 {
		return fmt.Sprintf("%q", values[i])
	}
	return fmt.Sprintf("%q at index %d", values[i], i)
}

// dedupe returns values with duplicates removed, order preserved: the
// existence checks pass ids straight to an AWS API, and a repeated id is at
// best wasted request payload and at worst an API error.
func dedupe(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// missingValuesError builds the diagnostic for values that the AWS API did not
// return. It is the shape CloudFormation's own validation error has: name the
// type, name the values, and say where they were looked for.
func missingValuesError(typeName string, missing []string) error {
	sort.Strings(missing)
	return fmt.Errorf(
		"%s %s %s not exist in this account and region; CloudFormation rejects a "+
			"%s parameter whose value does not resolve to a real resource. Set `validate = false` to "+
			"skip this existence check and keep only the syntactic one",
		typeName, quoteAll(missing), plural(len(missing), "does", "do"), typeName,
	)
}

// describeCallError wraps an AWS API failure from an existence check, naming
// the permission the check needs so a denied call is self-explanatory.
func describeCallError(typeName, api, permission string, err error) error {
	return fmt.Errorf(
		"calling %s to check that the resolved %s value exists (requires the %s permission; "+
			"set `validate = false` to skip the existence check): %w",
		api, typeName, permission, err)
}

func quoteAll(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, ", ")
}

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}

// missingFrom returns the values that are not in found.
func missingFrom(values []string, found map[string]bool) []string {
	var missing []string
	for _, v := range values {
		if !found[v] {
			missing = append(missing, v)
		}
	}
	return missing
}

func existsAvailabilityZoneNames(ctx context.Context, v *cfnTypeValidator, values []string) error {
	if v.EC2 == nil {
		return errNoEC2Client(cfnValueTypeAvailabilityZoneName)
	}
	out, err := v.EC2.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{ZoneNames: values})
	if err != nil {
		return describeCallError(cfnValueTypeAvailabilityZoneName, "EC2 DescribeAvailabilityZones", "ec2:DescribeAvailabilityZones", err)
	}
	found := map[string]bool{}
	if out != nil {
		for _, az := range out.AvailabilityZones {
			found[aws.ToString(az.ZoneName)] = true
		}
	}
	if missing := missingFrom(values, found); len(missing) > 0 {
		return missingValuesError(cfnValueTypeAvailabilityZoneName, missing)
	}
	return nil
}

func existsImageIDs(ctx context.Context, v *cfnTypeValidator, values []string) error {
	if v.EC2 == nil {
		return errNoEC2Client(cfnValueTypeImageID)
	}
	out, err := v.EC2.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: values})
	if err != nil {
		return describeCallError(cfnValueTypeImageID, "EC2 DescribeImages", "ec2:DescribeImages", err)
	}
	found := map[string]bool{}
	if out != nil {
		for _, img := range out.Images {
			found[aws.ToString(img.ImageId)] = true
		}
	}
	if missing := missingFrom(values, found); len(missing) > 0 {
		return missingValuesError(cfnValueTypeImageID, missing)
	}
	return nil
}

func existsInstanceIDs(ctx context.Context, v *cfnTypeValidator, values []string) error {
	if v.EC2 == nil {
		return errNoEC2Client(cfnValueTypeInstanceID)
	}
	out, err := v.EC2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: values})
	if err != nil {
		return describeCallError(cfnValueTypeInstanceID, "EC2 DescribeInstances", "ec2:DescribeInstances", err)
	}
	found := map[string]bool{}
	if out != nil {
		for _, reservation := range out.Reservations {
			for _, instance := range reservation.Instances {
				found[aws.ToString(instance.InstanceId)] = true
			}
		}
	}
	if missing := missingFrom(values, found); len(missing) > 0 {
		return missingValuesError(cfnValueTypeInstanceID, missing)
	}
	return nil
}

func existsKeyPairNames(ctx context.Context, v *cfnTypeValidator, values []string) error {
	if v.EC2 == nil {
		return errNoEC2Client(cfnValueTypeKeyPairKeyName)
	}
	out, err := v.EC2.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{KeyNames: values})
	if err != nil {
		return describeCallError(cfnValueTypeKeyPairKeyName, "EC2 DescribeKeyPairs", "ec2:DescribeKeyPairs", err)
	}
	found := map[string]bool{}
	if out != nil {
		for _, kp := range out.KeyPairs {
			found[aws.ToString(kp.KeyName)] = true
		}
	}
	if missing := missingFrom(values, found); len(missing) > 0 {
		return missingValuesError(cfnValueTypeKeyPairKeyName, missing)
	}
	return nil
}

func existsSecurityGroupIDs(ctx context.Context, v *cfnTypeValidator, values []string) error {
	if v.EC2 == nil {
		return errNoEC2Client(cfnValueTypeSecurityGroupID)
	}
	out, err := v.EC2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: values})
	if err != nil {
		return describeCallError(cfnValueTypeSecurityGroupID, "EC2 DescribeSecurityGroups", "ec2:DescribeSecurityGroups", err)
	}
	found := map[string]bool{}
	if out != nil {
		for _, sg := range out.SecurityGroups {
			found[aws.ToString(sg.GroupId)] = true
		}
	}
	if missing := missingFrom(values, found); len(missing) > 0 {
		return missingValuesError(cfnValueTypeSecurityGroupID, missing)
	}
	return nil
}

func existsSecurityGroupNames(ctx context.Context, v *cfnTypeValidator, values []string) error {
	if v.EC2 == nil {
		return errNoEC2Client(cfnValueTypeSecurityGroupGroupName)
	}
	// Security group names are not unique across VPCs, so they are looked up
	// through the group-name filter rather than the GroupNames field (which
	// is EC2-Classic/default-VPC only).
	out, err := v.EC2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{{Name: aws.String("group-name"), Values: values}},
	})
	if err != nil {
		return describeCallError(cfnValueTypeSecurityGroupGroupName, "EC2 DescribeSecurityGroups", "ec2:DescribeSecurityGroups", err)
	}
	found := map[string]bool{}
	if out != nil {
		for _, sg := range out.SecurityGroups {
			found[aws.ToString(sg.GroupName)] = true
		}
	}
	if missing := missingFrom(values, found); len(missing) > 0 {
		return missingValuesError(cfnValueTypeSecurityGroupGroupName, missing)
	}
	return nil
}

func existsSubnetIDs(ctx context.Context, v *cfnTypeValidator, values []string) error {
	if v.EC2 == nil {
		return errNoEC2Client(cfnValueTypeSubnetID)
	}
	out, err := v.EC2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: values})
	if err != nil {
		return describeCallError(cfnValueTypeSubnetID, "EC2 DescribeSubnets", "ec2:DescribeSubnets", err)
	}
	found := map[string]bool{}
	if out != nil {
		for _, subnet := range out.Subnets {
			found[aws.ToString(subnet.SubnetId)] = true
		}
	}
	if missing := missingFrom(values, found); len(missing) > 0 {
		return missingValuesError(cfnValueTypeSubnetID, missing)
	}
	return nil
}

func existsVolumeIDs(ctx context.Context, v *cfnTypeValidator, values []string) error {
	if v.EC2 == nil {
		return errNoEC2Client(cfnValueTypeVolumeID)
	}
	out, err := v.EC2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: values})
	if err != nil {
		return describeCallError(cfnValueTypeVolumeID, "EC2 DescribeVolumes", "ec2:DescribeVolumes", err)
	}
	found := map[string]bool{}
	if out != nil {
		for _, vol := range out.Volumes {
			found[aws.ToString(vol.VolumeId)] = true
		}
	}
	if missing := missingFrom(values, found); len(missing) > 0 {
		return missingValuesError(cfnValueTypeVolumeID, missing)
	}
	return nil
}

func existsVPCIDs(ctx context.Context, v *cfnTypeValidator, values []string) error {
	if v.EC2 == nil {
		return errNoEC2Client(cfnValueTypeVPCID)
	}
	out, err := v.EC2.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: values})
	if err != nil {
		return describeCallError(cfnValueTypeVPCID, "EC2 DescribeVpcs", "ec2:DescribeVpcs", err)
	}
	found := map[string]bool{}
	if out != nil {
		for _, vpc := range out.Vpcs {
			found[aws.ToString(vpc.VpcId)] = true
		}
	}
	if missing := missingFrom(values, found); len(missing) > 0 {
		return missingValuesError(cfnValueTypeVPCID, missing)
	}
	return nil
}

// existsHostedZoneIDs checks each hosted zone id with its own GetHostedZone
// call: Route 53 has no batch "describe these zones" API, so unlike the EC2
// checks this one costs one request per distinct value.
func existsHostedZoneIDs(ctx context.Context, v *cfnTypeValidator, values []string) error {
	if v.Route53 == nil {
		return fmt.Errorf(
			"cannot check that the resolved value exists as a %s: no Route 53 client was configured "+
				"(this is a bug in the cfncompat provider; please report it)", cfnValueTypeHostedZoneID)
	}
	var missing []string
	for _, id := range values {
		out, err := v.Route53.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: aws.String(id)})
		if err != nil {
			if isRoute53NoSuchHostedZone(err) {
				missing = append(missing, id)
				continue
			}
			return describeCallError(cfnValueTypeHostedZoneID, "Route 53 GetHostedZone", "route53:GetHostedZone", err)
		}
		if out == nil || out.HostedZone == nil {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return missingValuesError(cfnValueTypeHostedZoneID, missing)
	}
	return nil
}

// isRoute53NoSuchHostedZone reports whether err is Route 53's
// NoSuchHostedZone, i.e. "the zone does not exist" rather than "the call
// failed". Matched on the error string as well as the typed error so that a
// fake client in tests can report it without constructing SDK internals.
func isRoute53NoSuchHostedZone(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "NoSuchHostedZone")
}

func errNoEC2Client(typeName string) error {
	return fmt.Errorf(
		"cannot check that the resolved value exists as a %s: no EC2 client was configured "+
			"(this is a bug in the cfncompat provider; please report it)", typeName)
}
